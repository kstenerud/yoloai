// ABOUTME: Cobra "reset" command: re-copies workdir into the sandbox and resets
// ABOUTME: the diff baseline, with optional container restart and auto-attach.
package lifecycle

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

type resetOpts struct {
	noPrompt   bool
	restart    bool
	clearState bool
	keepCache  bool
	keepFiles  bool
	attach     bool
	debug      bool
}

func NewResetCmd() *cobra.Command {
	opts := &resetOpts{}
	cmd := &cobra.Command{
		Use:     "reset <name>",
		Short:   "Re-copy workdir into sandbox and reset diff baseline",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runReset(cmd, args, opts) },
	}

	cmd.Flags().BoolVar(&opts.noPrompt, "no-prompt", false, "Skip re-sending prompt after reset")
	cmd.Flags().BoolVar(&opts.restart, "restart", false, "Stop and restart the container")
	cmd.Flags().BoolVar(&opts.clearState, "clear-state", false, "Wipe agent runtime state (implies --restart)")
	cmd.Flags().BoolVar(&opts.keepCache, "keep-cache", false, "Preserve cache directory")
	cmd.Flags().BoolVar(&opts.keepFiles, "keep-files", false, "Preserve files directory")
	cmd.Flags().BoolVarP(&opts.attach, "attach", "a", false, "Auto-attach after restart (implies --restart)")

	return cmd
}

// runReset implements the reset command body.
func runReset(cmd *cobra.Command, args []string, opts *resetOpts) error {
	name, _, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()

	// --clear-state and --attach imply --restart
	if opts.clearState || opts.attach {
		opts.restart = true
	}

	if cliutil.JSONEnabled(cmd) && opts.attach {
		return yoerrors.NewUsageError("--json and --attach are incompatible")
	}

	if opts.attach {
		cliutil.SetTerminalTitle(name)
		defer cliutil.SetTerminalTitle("")
	}

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		slog.Info("resetting sandbox", "event", "sandbox.reset", "sandbox", name, "restart", opts.restart, "clear_state", opts.clearState) //nolint:gosec // G706: name is validated by ValidateName
		sb, err := c.Sandbox(name)
		if err != nil {
			return cliutil.SandboxErrorHint(name, err)
		}
		res, resetErr := sb.Reset(ctx, yoloai.ResetOptions{
			RestartContainer: opts.restart,
			ClearState:       opts.clearState,
			KeepCache:        opts.keepCache,
			KeepFiles:        opts.keepFiles,
			NoPrompt:         opts.noPrompt,
			Debug:            opts.debug,
		})
		if res != nil {
			cliutil.RenderNotices(cmd, res.Notices)
		}
		if resetErr != nil {
			return cliutil.SandboxErrorHint(name, resetErr)
		}
		slog.Info("sandbox reset complete", "event", "sandbox.reset.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
				"name":   name,
				"action": "reset",
			})
		}

		if opts.attach {
			return cliutil.WithTerminal(func(io yoloai.IOStreams) error {
				return sb.Agent().Attach(ctx, io)
			})
		}

		if opts.restart {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\nRun 'yoloai attach %s' to reconnect\n", name, name)
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\n", name)
		return err
	})
}
