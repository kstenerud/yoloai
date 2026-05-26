// ABOUTME: Cobra "attach" command: waits for tmux readiness then attaches the
// ABOUTME: user's terminal to the running sandbox session.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	yoloai "github.com/kstenerud/yoloai"
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

// runAttach implements the attach command body. After W-L8d's Sandbox.Attach
// landing, the heavy lifting (status check, waitForTmux, PTY-attach) lives in
// yoloai.Client.Attach; the CLI handles terminal title + IOStreams wiring.
func runAttach(cmd *cobra.Command, args []string, opts *attachOpts) error {
	if jsonEnabled(cmd) {
		return errJSONNotSupported("attach")
	}

	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}
	defer openCLIJSONLSink(name, cmd)()

	setTerminalTitle(name)
	defer setTerminalTitle("")

	backend := resolveBackendForSandbox(name)
	return withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		// --resume restarts the agent before attaching when the sandbox is
		// stopped or in a terminal state. Active/Idle sandboxes get an
		// in-place attach.
		if opts.resume {
			info, err := c.Inspect(ctx, name)
			if err != nil {
				return sandboxErrorHint(name, err)
			}
			if info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
				if err := c.Start(ctx, name, sandbox.StartOptions{Resume: true}); err != nil {
					return err
				}
			}
		}

		slog.Debug("attaching to sandbox", "event", "sandbox.attach", "sandbox", name) //nolint:gosec // G706: name is validated
		return c.Attach(ctx, name, cliIOStreams())
	})
}

// cliIOStreams returns an IOStreams bound to the calling process's terminal,
// sized from os.Stdin's PTY. Used by every CLI command that calls Client.Attach.
func cliIOStreams() yoloai.IOStreams {
	rows, cols, _ := pty.Getsize(os.Stdin)
	return yoloai.IOStreams{
		In:   os.Stdin,
		Out:  os.Stdout,
		Err:  os.Stderr,
		TTY:  true,
		Rows: rows,
		Cols: cols,
	}
}

// tmuxExecUser returns the user to use for tmux exec operations.
// Delegates to sandbox.ContainerUser which handles all cases:
//   - Podman --userns=keep-id: empty (use container default)
//   - gVisor: numeric host UID (gVisor resolves usernames from OCI manifest, not /etc/passwd)
//   - default: "yoloai"
func tmuxExecUser(meta *store.Meta) string {
	return sandbox.ContainerUser(meta)
}

// waitForTmux is the CLI-side wrapper around sandbox.WaitForAttachReady.
// Kept as a thin shim for callers that still hold a raw runtime; new code
// should construct a Client and call c.Attach which handles readiness
// polling internally.
func waitForTmux(ctx context.Context, rt runtime.Runtime, _, sandboxName string, timeout time.Duration, user string) error {
	return sandbox.WaitForAttachReady(ctx, rt, cliLayout(), sandboxName, user, timeout)
}

// readTmuxSocket is the CLI-side wrapper around sandbox.ReadTmuxSocket.
func readTmuxSocket(sandboxName string) string {
	return sandbox.ReadTmuxSocket(cliLayout(), sandboxName)
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
// Kept as a CLI-side shim for handlers that already hold a raw runtime
// (new.go, start.go, restart.go, reset.go). New code should reach Client.Attach
// via withClient + cliIOStreams() instead. The terminal-title machinery
// stays here because Client.Attach is library code and doesn't touch UI.
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
