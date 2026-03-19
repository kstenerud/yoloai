package containerdrt

// ABOUTME: Container lifecycle operations — Create, Start, Stop, Remove, Inspect.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	runtimeoptions "github.com/containerd/containerd/api/types/runtimeoptions/v1"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
)

// kataConfigPath returns the Kata Containers configuration file path for the
// given shimv2 runtime type, or "" to use the shim's built-in default.
func kataConfigPath(containerRuntime string) string {
	switch containerRuntime {
	case "io.containerd.kata-fc.v2":
		return "/opt/kata/share/defaults/kata-containers/configuration-fc.toml"
	default: // io.containerd.kata.v2
		// Return "" to use the shim's default configuration (Dragonball VMM).
		// Overriding with a QEMU config causes the VM to crash on this setup.
		return ""
	}
}

// sandboxDirForName returns the sandbox directory path for a container name.
func sandboxDirForName(name string) string {
	// Strip the "yoloai-" prefix from the container name to get the sandbox name.
	sandboxName := strings.TrimPrefix(name, "yoloai-")
	return config.SandboxesDir() + "/" + sandboxName
}

// retryDelete calls ctr.Delete with WithSnapshotCleanup, retrying on transient
// errors to handle Kata Containers shim teardown lag (the shim may still be
// running briefly after the task exit event fires). Returns nil if the
// container is gone (either deleted or not found), error otherwise.
func retryDelete(ctx context.Context, ctr client.Container) error {
	const maxAttempts = 5
	const retryDelay = 2 * time.Second
	var lastErr error
	for i := range maxAttempts {
		if i > 0 {
			select {
			case <-time.After(retryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		lastErr = ctr.Delete(ctx, client.WithSnapshotCleanup)
		if lastErr == nil || errdefs.IsNotFound(lastErr) {
			return nil
		}
	}
	return lastErr
}

// Create creates a new containerd container from the given InstanceConfig.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	ctx = r.withNamespace(ctx)

	sandboxDir := sandboxDirForName(cfg.Name)

	// Set up network namespace and CNI.
	netnsPath, err := setupCNI(ctx, sandboxDir, cfg.Name)
	if err != nil {
		return fmt.Errorf("setup CNI: %w", err)
	}
	// Tear down CNI on any error after this point so the netns file and IPAM
	// lease are not left behind for the next run.
	var createErr error
	defer func() {
		if createErr != nil {
			_ = teardownCNI(ctx, sandboxDir)
		}
	}()

	// Look up the image — do not pull; EnsureImage() is responsible for that.
	img, err := r.client.GetImage(ctx, cfg.ImageRef)
	if err != nil {
		if errdefs.IsNotFound(err) {
			createErr = fmt.Errorf("image %q not found; run 'yoloai setup' to build it", cfg.ImageRef)
			return createErr
		}
		createErr = fmt.Errorf("get image: %w", err)
		return createErr
	}

	// Select snapshotter: devmapper for Firecracker (vm-enhanced), overlayfs otherwise.
	snapshotter := "overlayfs"
	if cfg.ContainerRuntime == "io.containerd.kata-fc.v2" {
		snapshotter = "devmapper"
	}

	// Unpack the image into the snapshotter if not already done.
	// WithNewSnapshot requires the layer snapshot chain to already exist;
	// it does NOT unpack — it only calls Prepare(parent) on the final digest.
	if unpacked, err := img.IsUnpacked(ctx, snapshotter); err == nil && !unpacked {
		if err := img.Unpack(ctx, snapshotter); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				createErr = fmt.Errorf("unpack image: %w", err)
			} else {
				createErr = fmt.Errorf("unpack image: %w\n  Hint: image content may have been removed by containerd GC; run 'yoloai setup --force' to rebuild", err)
			}
			return createErr
		}
	}

	// Build OCI spec options.
	specOpts := []oci.SpecOpts{
		oci.WithDefaultSpec(),
		oci.WithImageConfig(img),
		// Replace the network namespace with our pre-created named netns.
		oci.WithLinuxNamespace(specs.LinuxNamespace{
			Type: specs.NetworkNamespace,
			Path: netnsPath,
		}),
	}

	if cfg.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(cfg.WorkingDir))
	}

	if len(cfg.CapAdd) > 0 {
		specOpts = append(specOpts, oci.WithAddedCapabilities(cfg.CapAdd))
	}

	// Always bind-mount a working resolv.conf so the container can resolve DNS.
	// Docker handles this automatically; raw containerd does not.
	// On systemd-resolved hosts (Ubuntu), /etc/resolv.conf → stub-resolv.conf
	// which contains nameserver 127.0.0.53 — unreachable from inside the VM.
	// Use /run/systemd/resolve/resolv.conf instead, which has the real upstream
	// nameservers. Fall back to /etc/resolv.conf on non-systemd hosts.
	resolvConf := "/etc/resolv.conf"
	if _, err := os.Stat("/run/systemd/resolve/resolv.conf"); err == nil {
		resolvConf = "/run/systemd/resolve/resolv.conf"
	}
	extraMounts := []specs.Mount{
		{
			Type:        "bind",
			Source:      resolvConf,
			Destination: "/etc/resolv.conf",
			Options:     []string{"rbind", "ro"},
		},
	}

	// Convert user-specified mounts.
	for _, m := range cfg.Mounts {
		opts := []string{"rbind"}
		if m.ReadOnly {
			opts = append(opts, "ro")
		} else {
			opts = append(opts, "rw")
		}
		extraMounts = append(extraMounts, specs.Mount{
			Type:        "bind",
			Source:      m.Source,
			Destination: m.Target,
			Options:     opts,
		})
	}
	specOpts = append(specOpts, oci.WithMounts(extraMounts))

	// Kata config: only override when a non-default config path is needed
	// (e.g. Firecracker). For the default kata.v2 runtime, pass nil to let
	// the shim use its built-in default (Dragonball VMM).
	var kataOpts interface{}
	if cfgPath := kataConfigPath(cfg.ContainerRuntime); cfgPath != "" {
		kataOpts = &runtimeoptions.Options{ConfigPath: cfgPath}
	}

	ctrOpts := []client.NewContainerOpts{
		client.WithSnapshotter(snapshotter),
		client.WithNewSnapshot(cfg.Name, img),
		client.WithNewSpec(specOpts...),
		client.WithRuntime(cfg.ContainerRuntime, kataOpts),
	}

	// Pre-clear any stale container with this name from a previous failed run.
	// Use retryDelete to handle Kata shim teardown lag (same as in Remove).
	if existingCtr, loadErr := r.client.LoadContainer(ctx, cfg.Name); loadErr == nil {
		if err := retryDelete(ctx, existingCtr); err != nil && !errdefs.IsNotFound(err) {
			createErr = fmt.Errorf("stale container %q could not be deleted: %w", cfg.Name, err)
			return createErr
		}
	}
	// Also pre-clear any stale snapshot that may have been orphaned (e.g. if the
	// container was deleted but snapshot cleanup failed due to permissions or a crash).
	_ = r.client.SnapshotService(snapshotter).Remove(ctx, cfg.Name)

	if _, err := r.client.NewContainer(ctx, cfg.Name, ctrOpts...); err != nil {
		createErr = fmt.Errorf("create container: %w", err)
		return createErr
	}

	return nil
}

// Start starts a previously created (or stopped) containerd container.
// Returns nil if already running.
func (r *Runtime) Start(ctx context.Context, name string) error {
	ctx = r.withNamespace(ctx)

	ctr, err := r.client.LoadContainer(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runtime.ErrNotFound
		}
		return fmt.Errorf("load container: %w", err)
	}

	// Check for an existing task: a stopped task must be deleted before
	// creating a new one.
	if existingTask, taskErr := ctr.Task(ctx, nil); taskErr == nil {
		status, _ := existingTask.Status(ctx)
		if status.Status == client.Running {
			return nil // already running
		}
		// Task exists but is stopped — delete it before creating a new task.
		_, _ = existingTask.Delete(ctx)
	}

	// Create task with null IO — agent logs go to bind-mounted log.txt.
	task, err := ctr.NewTask(ctx, cio.NullIO)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	if err := task.Start(ctx); err != nil {
		_, _ = task.Delete(ctx)
		return fmt.Errorf("start task: %w", err)
	}

	// task.Start returns once the shim acknowledges the RPC, but for
	// slow-starting runtimes (e.g. Kata Containers which boots a full VM)
	// the task may still be in Created state for many seconds. Poll until
	// it reaches Running or Stopped so that callers can rely on the
	// returned nil meaning "container is actually running".
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	for {
		status, statusErr := task.Status(ctx)
		if statusErr == nil {
			switch status.Status {
			case client.Running:
				return nil
			case client.Stopped:
				_, _ = task.Delete(ctx)
				return fmt.Errorf("task exited immediately after start (exit code: %d)", status.ExitStatus)
			}
		}
		select {
		case <-ticker.C:
			// poll again
		case <-timer.C:
			return fmt.Errorf("task did not reach running state within 60s")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Stop stops a running containerd container. Returns nil if already stopped or not found.
func (r *Runtime) Stop(ctx context.Context, name string) error {
	ctx = r.withNamespace(ctx)

	sandboxDir := sandboxDirForName(name)

	ctr, err := r.client.LoadContainer(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil // container gone — still teardown CNI
		}
		return fmt.Errorf("load container: %w", err)
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			// No task — container was never started or already stopped.
			return r.teardownCNIForSandbox(ctx, sandboxDir)
		}
		return fmt.Errorf("load task: %w", err)
	}

	// Register Wait before Kill to avoid race (shim buffers exit events either way).
	exitCh, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait task: %w", err)
	}

	status, _ := task.Status(ctx)
	if status.Status != client.Stopped {
		_ = task.Kill(ctx, syscall.SIGTERM)
		select {
		case <-exitCh:
		case <-time.After(10 * time.Second):
			_ = task.Kill(ctx, syscall.SIGKILL)
			<-exitCh
		}
	}

	_, _ = task.Delete(ctx)

	return r.teardownCNIForSandbox(ctx, sandboxDir)
}

// Remove removes a containerd container. Returns nil if already removed.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	ctx = r.withNamespace(ctx)

	sandboxDir := sandboxDirForName(name)

	// Stop first (idempotent).
	if err := r.Stop(ctx, name); err != nil {
		return err
	}

	ctr, err := r.client.LoadContainer(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			// Container was never created (e.g. previous run failed before
			// NewContainer). Still clean up CNI/netns which may have been
			// set up before the failure.
			return r.teardownCNIForSandbox(ctx, sandboxDir)
		}
		return fmt.Errorf("load container: %w", err)
	}

	// Retry Delete to handle Kata Containers shim teardown lag: the task exit
	// event fires before the kata-shim fully releases the container, so an
	// immediate Delete may fail with a transient error.
	if err := retryDelete(ctx, ctr); err != nil {
		return fmt.Errorf("delete container: %w", err)
	}

	// Idempotent — CNI may already be torn down by Stop.
	return r.teardownCNIForSandbox(ctx, sandboxDir)
}

// Inspect returns the current state of a containerd container.
func (r *Runtime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	ctx = r.withNamespace(ctx)

	ctr, err := r.client.LoadContainer(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runtime.InstanceInfo{}, runtime.ErrNotFound
		}
		return runtime.InstanceInfo{}, fmt.Errorf("inspect container: %w", err)
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runtime.InstanceInfo{Running: false}, nil
		}
		return runtime.InstanceInfo{}, fmt.Errorf("load task: %w", err)
	}

	status, err := task.Status(ctx)
	if err != nil {
		return runtime.InstanceInfo{}, fmt.Errorf("task status: %w", err)
	}

	return runtime.InstanceInfo{
		Running: status.Status == client.Running,
	}, nil
}

// teardownCNIForSandbox is a helper that calls teardownCNI with the non-namespaced ctx.
func (r *Runtime) teardownCNIForSandbox(ctx context.Context, sandboxDir string) error {
	return teardownCNI(ctx, sandboxDir)
}
