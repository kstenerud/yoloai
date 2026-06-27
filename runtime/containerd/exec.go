//go:build linux

package containerdrt

// ABOUTME: Command execution inside containerd containers — non-interactive and interactive (PTY).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	specs "github.com/opencontainers/runtime-spec/specs-go"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// AttachCommand returns the command to attach to the tmux session.
// For containerd/kata, stty is run first to set terminal dimensions on the PTY
// slave before tmux queries them via TIOCGWINSZ. The kata-agent creates the PTY
// inside the VM, and the ConsoleSize/Resize RPC may not propagate to the slave
// before tmux reads the size — stty ensures the dimensions are correct.
func (r *Runtime) AttachCommand(tmuxSocket string, rows, cols int, _ runtime.IsolationMode) []string {
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

	ctr, task, err := r.loadContainerAndTask(ctx, name)
	if err != nil {
		return runtime.ExecResult{}, err
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

	// Trim to honor the ExecResult.Stdout contract (whitespace-trimmed), so
	// containerd matches docker/apple/seatbelt/tart — all of which trim via their
	// Exec impl or the shared runtime.RunCmdExec helper. Without this, callers
	// comparing exec output had to special-case a trailing newline on containerd.
	result := runtime.ExecResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		ExitCode: int(exitStatus.ExitCode()),
	}

	if exitStatus.ExitCode() != 0 {
		return result, fmt.Errorf("exec exited with code %d: %s", exitStatus.ExitCode(), stderr.String())
	}

	return result, nil
}

// InteractiveExec runs a command inside a containerd container, wiring
// the supplied IOStreams to the shim's PTY/pipe. For io.TTY=true the
// shim allocates a PTY (terminal=true on the FIFO set) and we bridge
// it to io.In/Out (stderr is folded onto stdout, matching the PTY model).
// For io.TTY=false we still bridge through FIFOs but with no PTY on
// the remote side; stderr stays separate.
//
// io.In/Out/Err are treated as opaque byte streams — the library never
// inspects io.In's FD, sets raw mode, or installs signal handlers (§12:
// that reads live host-terminal state). The caller manages its own terminal
// (raw mode, etc.) before handing the streams in.
//
// Initial PTY geometry comes from io.Rows/io.Cols (zero → backend default).
// Live resizes arrive as TermSize values on io.Resize, which the caller
// drives from its own event source (SIGWINCH, a websocket message, …).
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string, io runtime.IOStreams) error {
	ctx = r.withNamespace(ctx)

	ctr, task, err := r.loadContainerAndTask(ctx, name)
	if err != nil {
		return err
	}

	process, exitCh, err := startInteractiveExec(ctx, r.layout, task, ctr, cmd, user, workDir, io)
	if err != nil {
		return err
	}
	defer func() { _, _ = process.Delete(ctx) }()

	if io.Rows > 0 && io.Cols > 0 {
		_ = process.Resize(ctx, uint32(io.Cols), uint32(io.Rows)) //nolint:gosec // G115: terminal dimensions fit uint32
	}
	if io.Resize != nil {
		go forwardResizes(ctx, process, io.Resize)
	}

	// Surface the inner exit code as *runtime.ExecError (not a bare nil) so the
	// errors.As-based callers — Sandbox.Exec, then the CLI's os.Exit — see it.
	// Dropping it here makes `yoloai exec <box> -- false` exit 0 on this backend
	// only. Mirrors the non-interactive Exec, which reads ExitCode() the same way.
	exitStatus := <-exitCh
	if code := int(exitStatus.ExitCode()); code != 0 {
		return &runtime.ExecError{ExitCode: code}
	}
	return nil
}

// forwardResizes applies caller-supplied geometry updates to the remote PTY
// until the channel closes or ctx is cancelled (the latter fires when
// InteractiveExec returns and its derived context is torn down).
func forwardResizes(ctx context.Context, process client.Process, resize <-chan runtime.TermSize) {
	for {
		select {
		case <-ctx.Done():
			return
		case sz, ok := <-resize:
			if !ok {
				return
			}
			if sz.Rows > 0 && sz.Cols > 0 {
				_ = process.Resize(ctx, uint32(sz.Cols), uint32(sz.Rows)) //nolint:gosec // G115: terminal dimensions fit uint32
			}
		}
	}
}

// loadContainerAndTask loads a container and its task from containerd, returning
// ErrNotFound if the container is missing and ErrNotRunning if no task exists.
func (r *Runtime) loadContainerAndTask(ctx context.Context, name string) (client.Container, client.Task, error) {
	ctr, err := r.client.LoadContainer(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, nil, runtime.ErrNotFound
		}
		return nil, nil, fmt.Errorf("load container: %w", err)
	}
	task, err := ctr.Task(ctx, nil)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, nil, runtime.ErrNotRunning
		}
		return nil, nil, fmt.Errorf("load task: %w", err)
	}
	return ctr, task, nil
}

// startInteractiveExec creates the FIFO set, builds the process spec, and starts the exec.
// Returns the process, an exit channel, and any error.
func startInteractiveExec(ctx context.Context, layout config.Layout, task client.Task, ctr client.Container, cmd []string, user, workDir string, io runtime.IOStreams) (client.Process, <-chan client.ExitStatus, error) {
	fifoDir, err := layout.MkdirTemp("yoloai-exec-")
	if err != nil {
		return nil, nil, fmt.Errorf("create FIFO dir: %w", err)
	}

	execID := fmt.Sprintf("exec-interactive-%d", os.Getpid())
	// terminal=true on the FIFO set when io.TTY: tells the shim to
	// allocate a PTY on the container side. For non-TTY execs, plain
	// pipes (stderr is meaningful, not folded onto stdout).
	fifoSet, err := cio.NewFIFOSetInDir(fifoDir, execID, io.TTY)
	if err != nil {
		_ = os.RemoveAll(fifoDir) // best-effort cleanup
		return nil, nil, fmt.Errorf("create FIFO set: %w", err)
	}

	// For PTY execs stderr is folded onto stdout (the PTY model). For
	// non-PTY we wire io.Err to the third stream.
	var ioAttach cio.Attach
	if io.TTY {
		ioAttach = cio.NewAttach(cio.WithTerminal, cio.WithStreams(io.In, io.Out, nil))
	} else {
		ioAttach = cio.NewAttach(cio.WithStreams(io.In, io.Out, io.Err))
	}

	// For interactive PTY execs, TERM must be set so ncurses/tmux can initialize.
	// The terminal type is caller-supplied (io.Term) — the library never reads
	// the process's own $TERM (§12: in a daemon that's the daemon's terminal,
	// not the principal's). Empty defaults to a safe modern terminal.
	env := containerEnv(ctx, ctr)
	termVal := io.Term
	if termVal == "" {
		termVal = "xterm-256color"
	}
	env = append(env, "TERM="+termVal)

	processSpec := buildInteractiveProcessSpec(cmd, user, workDir, env, io)

	process, err := task.Exec(ctx, execID, processSpec, func(id string) (cio.IO, error) {
		return ioAttach(fifoSet)
	})
	if err != nil {
		_ = os.RemoveAll(fifoDir) // best-effort cleanup
		return nil, nil, fmt.Errorf("exec create: %w", err)
	}
	// fifoDir cleanup handled by caller via process.Delete

	exitCh, err := process.Wait(ctx)
	if err != nil {
		_, _ = process.Delete(ctx)
		_ = os.RemoveAll(fifoDir) // best-effort cleanup
		return nil, nil, fmt.Errorf("exec wait: %w", err)
	}

	if err := process.Start(ctx); err != nil {
		_, _ = process.Delete(ctx)
		_ = os.RemoveAll(fifoDir) // best-effort cleanup
		return nil, nil, fmt.Errorf("exec start: %w", err)
	}

	return process, exitCh, nil
}

// buildInteractiveProcessSpec constructs the OCI process spec for an exec.
// io.TTY drives Terminal; io.Rows/io.Cols (when both non-zero) drive the
// initial ConsoleSize.
func buildInteractiveProcessSpec(cmd []string, user, workDir string, env []string, io runtime.IOStreams) *specs.Process {
	processSpec := &specs.Process{
		Args:     cmd,
		Cwd:      "/",
		Terminal: io.TTY,
		Env:      env,
	}
	if workDir != "" {
		processSpec.Cwd = workDir
	}
	if user != "" {
		processSpec.User = specs.User{Username: user}
	}
	// Set initial PTY size when the caller supplied one. Without this the
	// PTY starts at the shim default (e.g. 0×0) and tmux reads that size
	// before our post-start Resize call arrives.
	if io.TTY && io.Rows > 0 && io.Cols > 0 {
		processSpec.ConsoleSize = &specs.Box{
			Width:  uint(io.Cols), //nolint:gosec // G115: terminal dimensions fit in uint
			Height: uint(io.Rows), //nolint:gosec // G115
		}
	}
	return processSpec
}
