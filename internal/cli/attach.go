// ABOUTME: Cobra "attach" command: waits for tmux readiness then attaches the
// ABOUTME: user's terminal to the running sandbox session.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/spf13/cobra"
)

type attachOpts struct {
	resume bool
}

func newAttachCmd() *cobra.Command {
	opts := &attachOpts{}
	cmd := &cobra.Command{
		Use:     "attach <name>",
		Short:   "Attach to a sandbox's session (tmux)",
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runAttach(cmd, args, opts) },
	}

	cmd.Flags().BoolVar(&opts.resume, "resume", false, "Restart agent with resume prompt before attaching")

	return cmd
}

// runAttach implements the attach command body.
func runAttach(cmd *cobra.Command, args []string, opts *attachOpts) error {
	if jsonEnabled(cmd) {
		return errJSONNotSupported("attach")
	}

	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}
	defer openCLIJSONLSink(name, cmd)()

	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		return attachInRuntime(cmd, ctx, rt, name, opts)
	})
}

// attachInRuntime resolves sandbox status and attaches to its tmux session.
func attachInRuntime(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string, opts *attachOpts) error {
	info, err := sandbox.InspectSandbox(ctx, cliLayout(), rt, name)
	if err != nil {
		return sandboxErrorHint(name, err)
	}

	containerName := store.InstanceName(name)
	user := tmuxExecUser(info.Meta)

	if err := checkAttachStatus(info.Status, name, opts.resume); err != nil {
		return err
	}

	// --resume: restart agent before attaching
	if opts.resume && info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
		mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr(), sandbox.WithLayout(cliLayout()))
		if err := mgr.Start(ctx, name, sandbox.StartOptions{Resume: true}); err != nil {
			return err
		}
		if err := waitForTmux(ctx, rt, containerName, name, 300*time.Second, user); err != nil {
			return fmt.Errorf("waiting for tmux session: %w", err)
		}
	}

	slog.Debug("attaching to tmux session", "event", "sandbox.attach", "container", containerName) //nolint:gosec // G706: containerName comes from trusted sandbox metadata
	return attachToSandbox(ctx, rt, containerName, name, user)
}

// checkAttachStatus returns an error if the sandbox status does not allow attach.
func checkAttachStatus(status sandbox.Status, name string, resume bool) error {
	switch status {
	case sandbox.StatusActive, sandbox.StatusIdle, sandbox.StatusDone, sandbox.StatusFailed:
		return nil // OK — user can attach to see output
	case sandbox.StatusStopped:
		if resume {
			return nil
		}
	case sandbox.StatusRemoved, sandbox.StatusBroken, sandbox.StatusUnavailable:
		// fall through to the not-running error
	}
	return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
}

// tmuxExecUser returns the user to use for tmux exec operations.
// Delegates to sandbox.ContainerUser which handles all cases:
//   - Podman --userns=keep-id: empty (use container default)
//   - gVisor: numeric host UID (gVisor resolves usernames from OCI manifest, not /etc/passwd)
//   - default: "yoloai"
func tmuxExecUser(meta *store.Meta) string {
	return sandbox.ContainerUser(meta)
}

// readTmuxSocket returns the tmux socket path configured for a sandbox, or
// empty string if not set (backend does not use a custom socket).
func readTmuxSocket(sandboxName string) string {
	data, err := os.ReadFile(store.RuntimeConfigFilePath(cliLayout().SandboxDir(sandboxName))) //nolint:gosec // G304: path from trusted sandbox dir
	if err != nil {
		return ""
	}
	var cfg struct {
		TmuxSocket string `json:"tmux_socket"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.TmuxSocket
}

// waitForTmux polls until the agent has been launched inside the container.
// Returns early if the container stops running or the context is cancelled.
//
// Detection strategy (both are checked on each poll cycle):
//  1. sandbox.jsonl: read the container's structured log on the host and look
//     for the "sandbox.agent_launch" event (agent started) or
//     "sandbox.agent_not_found" (binary missing — still need to attach to show
//     the error). This is the primary check and works even when docker exec is
//     unreliable (e.g. gVisor on ARM64). Checking agent_launch rather than
//     tmux_start ensures we wait for lifecycle commands (onCreateCommand,
//     postStartCommand) to complete before attaching.
//  2. docker exec: run "tmux has-session -t main" inside the container.
//     This is the fallback for backends that don't write sandbox.jsonl.
func waitForTmux(ctx context.Context, rt runtime.Runtime, containerName, sandboxName string, timeout time.Duration, user string) error {
	jsonlPath := store.SandboxJSONLPath(cliLayout().SandboxDir(sandboxName))
	tmuxSocket := readTmuxSocket(sandboxName)
	deadline := time.Now().Add(timeout)
	var lastExecErr error
	for time.Now().Before(deadline) {
		ready, err := pollTmuxReady(ctx, rt, containerName, jsonlPath, tmuxSocket, user)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		lastExecErr = err
		if err := sleepOrCancel(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	slog.Debug("waitForTmux: timed out", "event", "sandbox.wait_tmux.timeout", //nolint:gosec // G706: slog uses structured logging, not vulnerable to log injection
		"container", containerName, "last_exec_err", lastExecErr)
	// Include container logs in the error to surface setup failures.
	if logs := rt.Logs(ctx, containerName, 50); logs != "" {
		return fmt.Errorf("tmux session not ready after %s\n\nContainer logs:\n%s", timeout, logs)
	}
	return fmt.Errorf("tmux session not ready after %s", timeout)
}

// pollTmuxReady performs one readiness check cycle. Returns (true, nil) when ready,
// (false, nil) to keep polling, or (false, err) on a hard error.
func pollTmuxReady(ctx context.Context, rt runtime.Runtime, containerName, jsonlPath, tmuxSocket, user string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	info, err := rt.Inspect(ctx, containerName)
	if err != nil || !info.Running {
		return false, fmt.Errorf("container %s is not running", containerName)
	}

	// Primary: check sandbox.jsonl for agent_launch or agent_not_found.
	// agent_launch is written by launch_agent() after lifecycle commands
	// finish, so attaching at this point means the user sees the agent
	// (or the "not found" error) immediately.
	// When sandbox.jsonl is readable, skip the exec fallback: the tmux
	// session is created before lifecycle commands run, so has-session
	// succeeds long before the agent is actually launched.
	if data, readErr := os.ReadFile(jsonlPath); readErr == nil { //nolint:gosec // G304: path from trusted sandbox dir
		if bytes.Contains(data, []byte(`"sandbox.agent_launch"`)) ||
			bytes.Contains(data, []byte(`"sandbox.agent_not_found"`)) {
			return true, nil
		}
		// Log exists but agent not yet launched — signal keep polling.
		return false, nil
	}

	// Fallback: docker exec tmux has-session.
	// Only reached when sandbox.jsonl is unreadable (old container images
	// that predate structured logging). The tmux session existing is
	// sufficient in that case because those images launch the agent
	// synchronously before writing any log.
	tmuxArgs := buildTmuxHasSessionArgs(tmuxSocket)
	_, execErr := rt.Exec(ctx, containerName, tmuxArgs, user)
	if execErr == nil {
		return true, nil
	}
	slog.Debug("waitForTmux: exec check failed", "event", "sandbox.wait_tmux.exec_fail", //nolint:gosec // G706: slog uses structured logging, not vulnerable to log injection
		"container", containerName, "err", execErr)
	return false, nil
}

// buildTmuxHasSessionArgs constructs the tmux has-session argument list.
func buildTmuxHasSessionArgs(tmuxSocket string) []string {
	args := []string{"tmux"}
	if tmuxSocket != "" {
		args = append(args, "-S", tmuxSocket)
	}
	return append(args, "has-session", "-t", "main")
}

// sleepOrCancel waits for the given duration or returns ctx.Err() if cancelled.
func sleepOrCancel(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// setTerminalTitle sets the terminal title for the host terminal.
// It emits an OSC 0 escape sequence (works for non-tmux terminals) and,
// if running inside a host tmux session, also renames the tmux window
// so the title shows in the tmux status bar.
// When title is empty, it restores the previous state (clears OSC title
// and unsets per-window tmux overrides to revert to user defaults).
func setTerminalTitle(title string) {
	fmt.Fprintf(os.Stdout, "\033]0;%s\007", title) //nolint:errcheck // best-effort terminal title

	// If inside a host tmux session, also set the window name.
	if os.Getenv("TMUX") == "" {
		return
	}
	if title != "" {
		// Disable automatic-rename (tmux tracking the foreground process name)
		// and allow-rename (programs sending escape sequences to rename the
		// window) so our title sticks while the sandbox is attached.
		exec.Command("tmux", "set-option", "-w", "automatic-rename", "off").Run() //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "set-option", "-w", "allow-rename", "off").Run()     //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "rename-window", title).Run()                        //nolint:errcheck,gosec // best-effort
	} else {
		// Unset per-window overrides so the window reverts to the user's
		// session/global defaults after detach.
		exec.Command("tmux", "set-option", "-wu", "automatic-rename").Run() //nolint:errcheck,gosec // best-effort
		exec.Command("tmux", "set-option", "-wu", "allow-rename").Run()     //nolint:errcheck,gosec // best-effort
	}
}

// attachToSandbox attaches to the tmux session in a running container.
// It sets the terminal title to the sandbox name and restores it on detach.
// The backend-specific attach command is built by rt.AttachCommand, which
// knows the correct PTY and terminal strategies for each runtime.
func attachToSandbox(ctx context.Context, rt runtime.Runtime, containerName, sandboxName string, user string) error {
	setTerminalTitle(sandboxName)
	defer setTerminalTitle("")

	meta, err := store.LoadMeta(cliLayout().SandboxDir(sandboxName))
	if err != nil {
		return fmt.Errorf("load sandbox metadata: %w", err)
	}

	sock := readTmuxSocket(sandboxName)
	// pty.Getsize returns (rows, cols, err) — named accordingly.
	rows, cols, _ := pty.Getsize(os.Stdin)
	cmd := rt.AttachCommand(sock, rows, cols, meta.Isolation)

	return rt.InteractiveExec(ctx, containerName, cmd, user, "")
}
