package containerdrt

// ABOUTME: Command execution inside containerd containers — non-interactive and interactive (PTY).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/creack/pty"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/term"

	"github.com/kstenerud/yoloai/runtime"
)

// Exec runs a command inside a running containerd container and returns the result.
func (r *Runtime) Exec(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error) {
	ctx = r.withNamespace(ctx)

	ctr, err := r.client.LoadContainer(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runtime.ExecResult{}, runtime.ErrNotFound
		}
		return runtime.ExecResult{}, fmt.Errorf("load container: %w", err)
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runtime.ExecResult{}, runtime.ErrNotRunning
		}
		return runtime.ExecResult{}, fmt.Errorf("load task: %w", err)
	}

	execID := fmt.Sprintf("exec-%d", os.Getpid())

	processSpec := &specs.Process{
		Args:     cmd,
		Cwd:      "/",
		Terminal: false,
	}
	if user != "" {
		processSpec.User = specs.User{Username: user}
	}

	var stdout, stderr bytes.Buffer
	ioCreator := cio.NewCreator(cio.WithStreams(nil, &stdout, &stderr))

	process, err := task.Exec(ctx, execID, processSpec, ioCreator)
	if err != nil {
		return runtime.ExecResult{}, fmt.Errorf("exec create: %w", err)
	}
	defer func() { _, _ = process.Delete(ctx) }()

	exitCh, err := process.Wait(ctx)
	if err != nil {
		return runtime.ExecResult{}, fmt.Errorf("exec wait: %w", err)
	}

	if err := process.Start(ctx); err != nil {
		return runtime.ExecResult{}, fmt.Errorf("exec start: %w", err)
	}

	exitStatus := <-exitCh

	result := runtime.ExecResult{
		Stdout:   stdout.String(),
		ExitCode: int(exitStatus.ExitCode()),
	}

	if exitStatus.ExitCode() != 0 {
		return result, fmt.Errorf("exec exited with code %d: %s", exitStatus.ExitCode(), stderr.String())
	}

	return result, nil
}

// InteractiveExec runs a command interactively (with PTY) inside a containerd container.
// The host terminal is set to raw mode; the shim creates a PTY inside the container
// and bridges it to named FIFOs that are attached here.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string) error {
	ctx = r.withNamespace(ctx)

	ctr, err := r.client.LoadContainer(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runtime.ErrNotFound
		}
		return fmt.Errorf("load container: %w", err)
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return runtime.ErrNotRunning
		}
		return fmt.Errorf("load task: %w", err)
	}

	// Set raw mode on the host terminal.
	//nolint:gosec // G115: uintptr->int conversion is safe for file descriptors on all supported platforms
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	//nolint:errcheck,gosec // G115: best-effort restore; uintptr->int safe for fd
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Create FIFO set in a temp dir with terminal=true.
	fifoDir, err := os.MkdirTemp("", "yoloai-exec-")
	if err != nil {
		return fmt.Errorf("create FIFO dir: %w", err)
	}
	defer os.RemoveAll(fifoDir) //nolint:errcheck // best-effort cleanup

	execID := fmt.Sprintf("exec-interactive-%d", os.Getpid())

	fifoSet, err := cio.NewFIFOSetInDir(fifoDir, execID, true /* terminal */)
	if err != nil {
		return fmt.Errorf("create FIFO set: %w", err)
	}

	// Attach using real stdin/stdout — the shim bridges them to the container PTY.
	ioAttach := cio.NewAttach(cio.WithTerminal, cio.WithStreams(os.Stdin, os.Stdout, nil))

	processSpec := &specs.Process{
		Args:     cmd,
		Cwd:      "/",
		Terminal: true,
	}
	if workDir != "" {
		processSpec.Cwd = workDir
	}
	if user != "" {
		processSpec.User = specs.User{Username: user}
	}

	process, err := task.Exec(ctx, execID, processSpec, func(id string) (cio.IO, error) {
		return ioAttach(fifoSet)
	})
	if err != nil {
		return fmt.Errorf("exec create: %w", err)
	}
	defer func() { _, _ = process.Delete(ctx) }()

	exitCh, err := process.Wait(ctx)
	if err != nil {
		return fmt.Errorf("exec wait: %w", err)
	}

	if err := process.Start(ctx); err != nil {
		return fmt.Errorf("exec start: %w", err)
	}

	// Send initial terminal size after start.
	if cols, rows, err := pty.Getsize(os.Stdin); err == nil {
		//nolint:gosec // G115: int->uint32 conversion is safe for terminal dimensions
		_ = process.Resize(ctx, uint32(cols), uint32(rows))
	}

	// Forward SIGWINCH (terminal resize) in a goroutine.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	go func() {
		for range sigCh {
			if cols, rows, err := pty.Getsize(os.Stdin); err == nil {
				//nolint:gosec // G115: int->uint32 conversion is safe for terminal dimensions
				_ = process.Resize(ctx, uint32(cols), uint32(rows))
			}
		}
	}()

	<-exitCh
	return nil
}
