// ABOUTME: `yoloai sandbox <name> terminal-snapshot` — capture the rendered agent
// ABOUTME: tmux pane for diagnostics. Non-interactive (no PTY); pipes stdout cleanly.
package sandboxcmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// terminalSnapshotScrollback is the default history depth (lines above
// the visible pane). DF3 settled on 200 — large enough to capture the
// last few agent turns, small enough to fit comfortably in a bug report
// and to render in a terminal scrollback buffer.
const terminalSnapshotScrollback = 200

// runTerminalSnapshot dispatches `yoloai sandbox <name> terminal-snapshot`.
// Writes the captured pane to stdout (plain text by default; ANSI form
// when --ansi is set). Exits non-zero if the sandbox isn't running so
// callers (smoke tests, bug reporters) can distinguish "no capture
// because not running" from "capture failed for another reason".
func runTerminalSnapshot(cmd *cobra.Command, name string, rest []string) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.ErrJSONNotSupported("terminal-snapshot")
	}

	ansi := false
	for _, arg := range rest {
		switch arg {
		case "--ansi":
			ansi = true
		case "":
			// ignore
		default:
			return sandbox.NewUsageError("unknown flag %q for terminal-snapshot (valid: --ansi)", arg)
		}
	}

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		snap, err := sb.CaptureTerminal(ctx, terminalSnapshotScrollback)
		if err != nil {
			// ErrContainerNotRunning is the documented "best-effort skip"
			// signal — let callers see it as a typed error rather than a
			// generic failure. Other errors propagate verbatim.
			if errors.Is(err, sandbox.ErrContainerNotRunning) {
				return err
			}
			return fmt.Errorf("terminal-snapshot: %w", err)
		}

		w := cmd.OutOrStdout()
		out := snap.Plain
		if ansi {
			out = snap.ANSI
		}
		if _, werr := w.Write(out); werr != nil {
			return fmt.Errorf("terminal-snapshot: write output: %w", werr)
		}
		return nil
	})
}
