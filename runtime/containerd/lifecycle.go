package containerdrt

// ABOUTME: Container lifecycle operations — Create, Start, Stop, Remove, Inspect.

import (
	"context"
	"errors"
	"fmt"
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
// given shimv2 runtime type.
func kataConfigPath(containerRuntime string) string {
	switch containerRuntime {
	case "io.containerd.kata-fc.v2":
		return "/opt/kata/share/defaults/kata-containers/configuration-fc.toml"
	default: // io.containerd.kata.v2
		return "/opt/kata/share/defaults/kata-containers/configuration-qemu.toml"
	}
}

// sandboxDirForName returns the sandbox directory path for a container name.
func sandboxDirForName(name string) string {
	// Strip the "yoloai-" prefix from the container name to get the sandbox name.
	sandboxName := strings.TrimPrefix(name, "yoloai-")
	return config.SandboxesDir() + "/" + sandboxName
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

	// Convert mounts.
	if len(cfg.Mounts) > 0 {
		mounts := make([]specs.Mount, 0, len(cfg.Mounts))
		for _, m := range cfg.Mounts {
			opts := []string{"rbind"}
			if m.ReadOnly {
				opts = append(opts, "ro")
			} else {
				opts = append(opts, "rw")
			}
			mounts = append(mounts, specs.Mount{
				Type:        "bind",
				Source:      m.Source,
				Destination: m.Target,
				Options:     opts,
			})
		}
		specOpts = append(specOpts, oci.WithMounts(mounts))
	}

	// Kata config passed via runtimeoptions.
	kataOpts := &runtimeoptions.Options{
		ConfigPath: kataConfigPath(cfg.ContainerRuntime),
	}

	ctrOpts := []client.NewContainerOpts{
		client.WithSnapshotter(snapshotter),
		client.WithNewSnapshot(cfg.Name, img),
		client.WithNewSpec(specOpts...),
		client.WithRuntime(cfg.ContainerRuntime, kataOpts),
	}

	// Pre-clear any stale container with this name from a previous failed run.
	if existingCtr, loadErr := r.client.LoadContainer(ctx, cfg.Name); loadErr == nil {
		_ = existingCtr.Delete(ctx, client.WithSnapshotCleanup)
	}

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

	return nil
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

	if err := ctr.Delete(ctx, client.WithSnapshotCleanup); err != nil {
		if errdefs.IsNotFound(err) {
			return r.teardownCNIForSandbox(ctx, sandboxDir)
		}
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
