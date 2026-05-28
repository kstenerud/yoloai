//go:build linux

package containerdrt

// ABOUTME: Command execution inside containerd containers — non-interactive and interactive (PTY).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/creack/pty"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/term"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// termSizeOf returns the terminal size of *f* as (rows, cols), or
// (0, 0) if it can't be determined. pty.Getsize returns (rows, cols, err)
// — named accordingly to prevent swapping.
func termSizeOf(f *os.File) (rows, cols int) {
	r, c, err := pty.Getsize(f)
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

	result := runtime.ExecResult{
		Stdout:   stdout.String(),
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
// When io.In is a real terminal *os.File, we set the host fd to raw mode
// so escape sequences (Ctrl-B, arrow keys) reach the remote PTY rather
// than being intercepted by the host's line discipline. Non-PTY io.In
// (typically *os.Pipe from an HTTP/MCP bridge) gets no raw-mode handling.
//
// Initial PTY geometry comes from io.Rows/io.Cols. Zero means "detect
// from io.In's FD if possible, else let the backend pick a default."
// SIGWINCH forwarding only fires when io.In is a real terminal — for
// virtual PTYs the embedder is responsible for resize forwarding.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string, io runtime.IOStreams) error {
	ctx = r.withNamespace(ctx)

	ctr, task, err := r.loadContainerAndTask(ctx, name)
	if err != nil {
		return err
	}

	// Raw mode is only meaningful when io.In is a real terminal FD on
	// the host. Skip silently for piped/virtual streams.
	if hostFile, ok := io.In.(*os.File); ok && term.IsTerminal(int(hostFile.Fd())) && io.TTY { //nolint:gosec // G115: fd is small int range
		oldState, terr := term.MakeRaw(int(hostFile.Fd())) //nolint:gosec // G115
		if terr != nil {
			return fmt.Errorf("set raw mode: %w", terr)
		}
		defer term.Restore(int(hostFile.Fd()), oldState) //nolint:errcheck,gosec // G115/G104: best-effort restore
	}

	process, exitCh, err := startInteractiveExec(ctx, task, ctr, cmd, user, workDir, io)
	if err != nil {
		return err
	}
	defer func() { _, _ = process.Delete(ctx) }()

	rows, cols := resolveTermSize(io)
	if rows > 0 {
		_ = process.Resize(ctx, uint32(cols), uint32(rows)) //nolint:gosec // G115: terminal dimensions fit uint32
	}

	if hostFile, ok := io.In.(*os.File); ok && term.IsTerminal(int(hostFile.Fd())) { //nolint:gosec // G115
		forwardSIGWINCH(ctx, process, hostFile)
	}

	<-exitCh
	return nil
}

// resolveTermSize returns the PTY geometry to send to the remote. If
// the caller supplied Rows/Cols, those win. Otherwise we detect from
// io.In if it's an *os.File. If neither, return (0,0) and let the
// backend pick its default.
func resolveTermSize(io runtime.IOStreams) (rows, cols int) {
	if io.Rows > 0 && io.Cols > 0 {
		return io.Rows, io.Cols
	}
	if hostFile, ok := io.In.(*os.File); ok {
		return termSizeOf(hostFile)
	}
	return 0, 0
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

// forwardSIGWINCH starts a goroutine that forwards SIGWINCH to the
// container process, sourcing the new size from the supplied host
// terminal file. Caller must have already verified hostFile is a TTY.
func forwardSIGWINCH(ctx context.Context, process client.Process, hostFile *os.File) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			if rows, cols := termSizeOf(hostFile); rows > 0 {
				_ = process.Resize(ctx, uint32(cols), uint32(rows)) //nolint:gosec // G115: int->uint32 conversion is safe for terminal dimensions
			}
		}
	}()
}

// startInteractiveExec creates the FIFO set, builds the process spec, and starts the exec.
// Returns the process, an exit channel, and any error.
func startInteractiveExec(ctx context.Context, task client.Task, ctr client.Container, cmd []string, user, workDir string, io runtime.IOStreams) (client.Process, <-chan client.ExitStatus, error) {
	fifoDir, err := os.MkdirTemp("", "yoloai-exec-")
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
	env := containerEnv(ctx, ctr)
	termVal := os.Getenv("TERM") //nolint:forbidigo // §12: propagate the controlling terminal's TERM into the interactive exec (UI), defaulted below
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
// io.TTY drives Terminal; io.Rows/io.Cols (with fallback to detecting from
// io.In's FD if it's a host terminal) drive ConsoleSize.
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
	// Set initial PTY size when known. Without this the PTY starts at
	// the shim default (e.g. 0×0) and tmux reads that size before our
	// post-start Resize call arrives.
	if io.TTY {
		if rows, cols := resolveTermSize(io); rows > 0 {
			processSpec.ConsoleSize = &specs.Box{
				Width:  uint(cols), //nolint:gosec // G115: terminal dimensions fit in uint
				Height: uint(rows), //nolint:gosec // G115
			}
		}
	}
	return processSpec
}
