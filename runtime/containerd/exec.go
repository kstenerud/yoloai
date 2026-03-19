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

// termSize returns the current terminal size as (rows, cols).
// Returns (0, 0) if the terminal size cannot be determined.
// pty.Getsize returns (rows, cols, err) — named accordingly to prevent swapping.
func termSize() (rows, cols int) {
	r, c, err := pty.Getsize(os.Stdin)
	if err != nil {
		return 0, 0
	}
	return r, c
}

// AttachCommand returns the command to attach to the tmux session.
// For containerd/kata, stty is run first to set terminal dimensions on the PTY
// slave before tmux queries them via TIOCGWINSZ. The kata-agent creates the PTY
// inside the VM, and the ConsoleSize/Resize RPC may not propagate to the slave
// before tmux reads the size — stty ensures the dimensions are correct.
func (r *Runtime) AttachCommand(tmuxSocket string, rows, cols int, _ string) []string {
	var tmuxCmd string
	if tmuxSocket != "" {
		tmuxCmd = fmt.Sprintf("exec /usr/bin/tmux -S %s attach -t main", tmuxSocket)
	} else {
		tmuxCmd = "exec /usr/bin/tmux attach -t main"
	}
	if rows > 0 && cols > 0 {
		tmuxCmd = fmt.Sprintf("stty cols %d rows %d 2>/dev/null; %s", cols, rows, tmuxCmd)
	}
	return []string{"/bin/sh", "-c", tmuxCmd}
}

// containerEnv returns the Env slice from the container's stored OCI spec.
// The Kata agent (inside the VM) executes processes in a clean environment — it does
// not inherit the running container's environment the way Docker daemon does. Without
// an explicit Env (including PATH), bare command names fail to resolve.
// Falls back to a standard Debian PATH if the spec cannot be read.
func containerEnv(ctx context.Context, ctr interface {
	Spec(context.Context) (*specs.Spec, error)
}) []string {
	const fallback = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	spec, err := ctr.Spec(ctx)
	if err != nil || spec.Process == nil || len(spec.Process.Env) == 0 {
		return []string{fallback}
	}
	return spec.Process.Env
}

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
		Env:      containerEnv(ctx, ctr),
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

	// For interactive PTY execs, TERM must be set so ncurses/tmux can
	// initialize. The container OCI spec does not include TERM (it's a
	// runtime property, not an image property). Use the host's TERM value
	// so the terminal type matches the PTY being bridged.
	env := containerEnv(ctx, ctr)
	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}
	env = append(env, "TERM="+term)

	processSpec := &specs.Process{
		Args:     cmd,
		Cwd:      "/",
		Terminal: true,
		Env:      env,
	}
	if workDir != "" {
		processSpec.Cwd = workDir
	}
	if user != "" {
		processSpec.User = specs.User{Username: user}
	}
	// Set the initial PTY size so the kata-agent creates the PTY at the correct
	// dimensions. Without this the PTY starts at the shim default (e.g. 0×0),
	// and tmux reads that size before our post-start Resize call arrives.
	if rows, cols := termSize(); rows > 0 {
		processSpec.ConsoleSize = &specs.Box{
			Width:  uint(cols), //nolint:gosec // G115: terminal dimensions fit in uint
			Height: uint(rows), //nolint:gosec // G115
		}
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
	if rows, cols := termSize(); rows > 0 {
		_ = process.Resize(ctx, uint32(cols), uint32(rows)) //nolint:gosec // G115: int->uint32 conversion is safe for terminal dimensions
	}

	// Forward SIGWINCH (terminal resize) in a goroutine.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	go func() {
		for range sigCh {
			if rows, cols := termSize(); rows > 0 {
				_ = process.Resize(ctx, uint32(cols), uint32(rows)) //nolint:gosec // G115: int->uint32 conversion is safe for terminal dimensions
			}
		}
	}()

	<-exitCh
	return nil
}
