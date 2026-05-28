// ABOUTME: Cobra "attach" command: waits for tmux readiness then attaches the
// ABOUTME: user's terminal to the running sandbox session.
package workflow

import (
	"context"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

type attachOpts struct {
	resume bool
}

func NewAttachCmd() *cobra.Command {
	opts := &attachOpts{}
	cmd := &cobra.Command{
		Use:     "attach <name>",
		Short:   "Attach to a sandbox's session (tmux)",
		GroupID: cliutil.GroupWorkflow,
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
	if cliutil.JSONEnabled(cmd) {
		return cliutil.ErrJSONNotSupported("attach")
	}

	name, _, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()

	cliutil.SetTerminalTitle(name)
	defer cliutil.SetTerminalTitle("")

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		// --resume restarts the agent before attaching when the sandbox is
		// stopped or in a terminal state. Active/Idle sandboxes get an
		// in-place attach.
		sb, err := c.Sandbox(name)
		if err != nil {
			return cliutil.SandboxErrorHint(name, err)
		}
		if opts.resume {
			info, err := sb.Inspect(ctx)
			if err != nil {
				return cliutil.SandboxErrorHint(name, err)
			}
			if info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
				if err := sb.Start(ctx, sandbox.StartOptions{Resume: true}); err != nil {
					return err
				}
			}
		}

		slog.Debug("attaching to sandbox", "event", "sandbox.attach", "sandbox", name) //nolint:gosec // G706: name is validated
		return sb.Attach(ctx, cliutil.IOStreams())
	})
}
