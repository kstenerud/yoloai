//go:build linux

package containerdrt

// ABOUTME: Container lifecycle operations — Create, Start, Stop, Remove, Inspect.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strconv"
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
func kataConfigPath(_ string) string {
	// Return "" for all runtimes to use the shim's built-in default configuration.
	//
	// io.containerd.kata-fc.v2 (Firecracker): the Rust shim (runtime-rs ≥ 3.x)
	// selects the Firecracker VMM automatically based on the runtime type without
	// needing an explicit config path. Passing the configuration-rs-fc.toml
	// explicitly causes "After 500 attempts" (kata-agent unreachable), while
	// omitting it (matching `ctr run` behavior) allows the VM to boot normally.
	//
	// io.containerd.kata.v2 (Dragonball): the shim's default is Dragonball VMM,
	// which works correctly without an explicit config path.
	return ""
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

// killStaleKataShims finds and kills any Kata Containers shim processes for the
// given container name that are orphaned (not registered with containerd). These
// are left behind when a shim start fails partway through — containerd cleans up
// its own task record but the shim process persists, holding an abstract Unix
// socket that blocks the next start attempt.
//
// The shim may be invoked with either the bare container name or the
// namespace-prefixed form (e.g. "yoloai-x" or "yoloai-yoloai-x") depending on
// the containerd version, so both patterns are matched.
//
// Returns true if any shims were killed. The function reads /proc to find shim
// processes with matching -id arguments and sends SIGKILL. Errors are silently
// ignored because the shim may already be gone by the time we read its cmdline.
func killStaleKataShims(namespace, name string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	namespacedName := namespace + "-" + name
	killed := false
	for _, e := range entries {
		if killKataShimEntry(e, name, namespacedName) {
			killed = true
		}
	}
	return killed
}

// killKataShimEntry checks whether a /proc entry is a Kata shim matching name
// or namespacedName and kills it if so. Returns true if a process was killed.
func killKataShimEntry(e os.DirEntry, name, namespacedName string) bool {
	if !e.IsDir() {
		return false
	}
	pid, err := strconv.Atoi(e.Name())
	if err != nil || pid <= 1 {
		return false
	}
	cmdlineData, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)) //nolint:gosec // G304: reading kernel proc file
	if err != nil {
		return false
	}
	args := strings.Split(string(cmdlineData), "\x00")
	if len(args) == 0 || !strings.Contains(args[0], "containerd-shim-kata") {
		return false
	}
	for i, arg := range args {
		if arg == "-id" && i+1 < len(args) {
			id := args[i+1]
			if id == name || id == namespacedName {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				return true
			}
		}
	}
	return false
}

// removeKataStateDir removes stale kata runtime-rs state that would prevent a
// new shim from starting. Two sources of EADDRINUSE are cleaned up:
//
//  1. /run/kata/<namespace>-<name>/ — the kata management socket directory.
//     The shim creates shim-monitor.sock here on startup; when the shim exits
//     abnormally the directory persists because file sockets are not
//     automatically released by the kernel (unlike abstract sockets).
//
//  2. /run/containerd/s/<sha256> — the containerd TTRPC socket that the shim
//     creates and binds at startup. The path is derived from a deterministic
//     formula: sha256(containerdSock + "/" + namespace + "/" + taskID), where
//     taskID = namespace + "-" + name (the namespace-prefixed form containerd
//     uses). When the shim dies without cleanup, this socket file persists too.
//     Containerd's "clean up dead shim" code tries to remove it but can fail
//     if the kata cleanup step returns an error first.
func removeKataStateDir(namespace, name string) {
	// 1. Kata management socket directory.
	// The runtime-rs shim creates /run/kata/<name>/ using the container name
	// directly (e.g. /run/kata/yoloai-x/), not the namespace-prefixed form.
	kataDir := fmt.Sprintf("/run/kata/%s", name)
	_ = os.RemoveAll(kataDir) //nolint:gosec // G304: path is from internal consts

	// 2. Containerd TTRPC shim socket.
	// Replicates containerd/containerd/v2/pkg/shim.SocketAddress() formula:
	//   path = filepath.Join(addressFlag, ns, id)
	//   socket = /run/containerd/s/<hex(sha256(path))>
	// where addressFlag = containerd's main socket, ns = namespace, id = name
	// (the container name, e.g. "yoloai-x"). The "id" is the task ID which
	// containerd sets to the container name — it is NOT additionally prefixed
	// with the namespace.
	socketPath := containerdSock + "/" + namespace + "/" + name
	d := sha256.Sum256([]byte(socketPath))
	// The kata shim (runtime-rs, written in Rust) formats the SHA256 hash with
	// uppercase hex. Go's %x is lowercase; use %X to match the actual filename.
	// Remove both cases defensively in case the format ever changes.
	shimSocketUpper := fmt.Sprintf("/run/containerd/s/%X", d)
	shimSocketLower := fmt.Sprintf("/run/containerd/s/%x", d)
	_ = os.Remove(shimSocketUpper) //nolint:gosec // G304: path is from internal consts
	_ = os.Remove(shimSocketLower) //nolint:gosec // G304: path is from internal consts
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

	// Look up the image — do not pull; Setup() is responsible for that.
	img, err := r.client.GetImage(ctx, cfg.ImageRef)
	if err != nil {
		if errdefs.IsNotFound(err) {
			createErr = fmt.Errorf("image %q not found; run 'yoloai system setup' to build it", cfg.ImageRef)
			return createErr
		}
		createErr = fmt.Errorf("get image: %w", err)
		return createErr
	}

	// Select snapshotter: use the caller-specified snapshotter, defaulting to overlayfs.
	snapshotter := cfg.Snapshotter
	if snapshotter == "" {
		snapshotter = "overlayfs"
	}

	// Unpack the image into the snapshotter if not already done.
	// WithNewSnapshot requires the layer snapshot chain to already exist;
	// it does NOT unpack — it only calls Prepare(parent) on the final digest.
	if err := ensureImageUnpacked(ctx, img, snapshotter); err != nil {
		createErr = err
		return createErr
	}

	// Build OCI spec options.
	specOpts := buildContainerSpecOpts(img, netnsPath, cfg)

	// Kata config: only override when a non-default config path is needed
	// (e.g. Firecracker). For the default kata.v2 runtime, pass nil to let
	// the shim use its built-in default (Dragonball VMM).
	var kataOpts any
	if cfgPath := kataConfigPath(cfg.ContainerRuntime); cfgPath != "" {
		kataOpts = &runtimeoptions.Options{ConfigPath: cfgPath}
	}

	ctrOpts := []client.NewContainerOpts{
		client.WithSnapshotter(snapshotter),
		client.WithNewSnapshot(cfg.Name, img),
		client.WithNewSpec(specOpts...),
		client.WithRuntime(cfg.ContainerRuntime, kataOpts),
	}

	// Pre-clear stale container, snapshot, and Kata shim state.
	if err := r.clearStaleContainerState(ctx, cfg.Name, snapshotter); err != nil {
		createErr = err
		return createErr
	}

	if _, err := r.client.NewContainer(ctx, cfg.Name, ctrOpts...); err != nil {
		createErr = fmt.Errorf("create container: %w", err)
		return createErr
	}

	return nil
}

// ensureImageUnpacked unpacks the image into the snapshotter if not already done.
// Returns a user-friendly error with a GC hint for non-context errors.
func ensureImageUnpacked(ctx context.Context, img client.Image, snapshotter string) error {
	unpacked, err := img.IsUnpacked(ctx, snapshotter)
	if err != nil || unpacked {
		return nil //nolint:nilerr // intentional: skip unpack if already done or can't check — let NewSnapshot fail if needed
	}
	if err := img.Unpack(ctx, snapshotter); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("unpack image: %w", err)
		}
		return fmt.Errorf("unpack image: %w\n  Hint: image content may have been removed by containerd GC; run 'yoloai system build --force' to rebuild", err)
	}
	return nil
}

// buildContainerSpecOpts assembles the OCI spec options for a new container.
func buildContainerSpecOpts(img client.Image, netnsPath string, cfg runtime.InstanceConfig) []oci.SpecOpts {
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

	if cfg.Privileged {
		specOpts = append(specOpts, oci.WithPrivileged)
	} else {
		if len(cfg.CapAdd) > 0 {
			specOpts = append(specOpts, oci.WithAddedCapabilities(cfg.CapAdd))
		}
		if cfg.Seccomp == "unconfined" {
			specOpts = append(specOpts, oci.WithSeccompUnconfined)
		}
	}

	return append(specOpts, oci.WithMounts(buildContainerMounts(cfg.Mounts)))
}

// buildContainerMounts builds the bind-mount list for a new container,
// including a working resolv.conf for DNS resolution.
func buildContainerMounts(mounts []runtime.MountSpec) []specs.Mount {
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

	for _, m := range mounts {
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
	return extraMounts
}

// clearStaleContainerState removes any stale container, snapshot, and Kata shim
// state left from a previous failed run.
func (r *Runtime) clearStaleContainerState(ctx context.Context, name, snapshotter string) error {
	// Pre-clear any stale container with this name from a previous failed run.
	// Use retryDelete to handle Kata shim teardown lag (same as in Remove).
	if existingCtr, loadErr := r.client.LoadContainer(ctx, name); loadErr == nil {
		if err := retryDelete(ctx, existingCtr); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("stale container %q could not be deleted: %w", name, err)
		}
	}
	// Also pre-clear any stale snapshot that may have been orphaned (e.g. if the
	// container was deleted but snapshot cleanup failed due to permissions or a crash).
	_ = r.client.SnapshotService(snapshotter).Remove(ctx, name)

	// Kill any orphaned Kata shim processes for this container name. These are
	// left behind when a previous NewTask() call spawned a shim that then failed
	// to start. Also remove the kata runtime-rs state directory: unlike abstract
	// Unix sockets (which the kernel releases on process exit), filesystem sockets
	// persist until explicitly deleted. The next shim start fails with EADDRINUSE
	// if /run/kata/<namespace>-<name>/shim-monitor.sock still exists on disk.
	if killStaleKataShims(r.namespace, name) {
		time.Sleep(500 * time.Millisecond)
	}
	removeKataStateDir(r.namespace, name)
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

	if alreadyRunning := cleanupStoppedTask(ctx, ctr); alreadyRunning {
		return nil
	}

	// Kill any orphaned Kata shim processes and remove stale runtime-rs state
	// directories before starting. The shim creates a management socket at
	// /run/kata/<namespace>-<name>/shim-monitor.sock on start; if a prior shim
	// exited without cleanup (crash, SIGKILL), the socket file persists and
	// causes bind() to fail with EADDRINUSE on the next start attempt.
	if killStaleKataShims(r.namespace, name) {
		time.Sleep(500 * time.Millisecond)
	}
	removeKataStateDir(r.namespace, name)

	// Create task with null IO — agent logs go to bind-mounted log.txt.
	task, err := createTaskWithRetry(ctx, ctr)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	if err := task.Start(ctx); err != nil {
		_, _ = task.Delete(ctx)
		return fmt.Errorf("start task: %w", err)
	}

	return waitForTaskRunning(ctx, task)
}

// cleanupStoppedTask checks for an existing task on the container.
// If the task is running it returns true (already running).
// If stopped, it deletes the task and returns false so a new task can be created.
func cleanupStoppedTask(ctx context.Context, ctr client.Container) (alreadyRunning bool) {
	existingTask, taskErr := ctr.Task(ctx, nil)
	if taskErr != nil {
		return false // no task — proceed to create
	}
	status, _ := existingTask.Status(ctx)
	if status.Status == client.Running {
		return true
	}
	// Task exists but is stopped — delete it before creating a new task.
	_, _ = existingTask.Delete(ctx)
	return false
}

// createTaskWithRetry creates a new task, retrying on "address in use" errors
// that indicate a stale Kata shim socket.
func createTaskWithRetry(ctx context.Context, ctr client.Container) (client.Task, error) {
	const shimMaxRetries = 5
	const shimRetryDelay = 2 * time.Second

	var task client.Task
	var createTaskErr error
	for attempt := range shimMaxRetries {
		if attempt > 0 {
			select {
			case <-time.After(shimRetryDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		task, createTaskErr = ctr.NewTask(ctx, cio.NullIO)
		if createTaskErr == nil {
			break
		}
		if !runtime.IsAddressInUse(createTaskErr) {
			break // non-retryable error
		}
	}
	return task, createTaskErr
}

// waitForTaskRunning polls until the task reaches Running or Stopped state.
// task.Start returns once the shim acknowledges the RPC, but for slow-starting
// runtimes (e.g. Kata Containers which boots a full VM) the task may still be
// in Created state for many seconds.
func waitForTaskRunning(ctx context.Context, task client.Task) error {
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
			default:
				// transitioning (Created/Paused/Pausing/Unknown) — poll again
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
