// ABOUTME: CLI command to restart a sandbox (stop + start).
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

type restartOpts struct {
	attach       bool
	resume       bool
	prompt       string
	promptFile   string
	isolation    string
	vscodeTunnel bool
}

func NewRestartCmd() *cobra.Command {
	opts := &restartOpts{}
	cmd := &cobra.Command{
		Use:     "restart <name>",
		Short:   "Restart the agent in an existing sandbox",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runRestart(cmd, args, opts) },
	}

	cmd.Flags().BoolVarP(&opts.attach, "attach", "a", false, "Auto-attach after restart")
	cmd.Flags().BoolVar(&opts.resume, "resume", false, "Re-feed original prompt with continuation preamble")
	cmd.Flags().StringVarP(&opts.prompt, "prompt", "p", "", "New prompt text (overwrites existing prompt)")
	cmd.Flags().StringVarP(&opts.promptFile, "prompt-file", "f", "", "File containing new prompt")
	cmd.Flags().StringVar(&opts.isolation, "isolation", "", "Override isolation mode (e.g. container-privileged for Docker-in-Docker)")
	cmd.Flags().BoolVar(&opts.vscodeTunnel, "vscode-tunnel", false, "Enable VS Code Remote Tunnel (persisted; tunnel starts with the restarted container)")

	cmd.MarkFlagsMutuallyExclusive("resume", "prompt")
	cmd.MarkFlagsMutuallyExclusive("resume", "prompt-file")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")

	return cmd
}

// runRestart implements the restart command body.
func runRestart(cmd *cobra.Command, args []string, opts *restartOpts) error {
	name, _, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()

	if cliutil.JSONEnabled(cmd) && opts.attach {
		return yoerrors.NewUsageError("--json and --attach are incompatible")
	}

	// Set terminal title early so it shows the sandbox name during restart.
	if opts.attach {
		cliutil.SetTerminalTitle(name)
		defer cliutil.SetTerminalTitle("")
	}

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		slog.Info("restarting sandbox", "event", "sandbox.restart", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		res, restartErr := sb.Restart(ctx, yoloai.StartOptions{
			Resume:       opts.resume,
			Prompt:       opts.prompt,
			PromptFile:   opts.promptFile,
			Isolation:    yoloai.IsolationMode(opts.isolation),
			VscodeTunnel: opts.vscodeTunnel,
		})
		if res != nil {
			cliutil.RenderNotices(cmd, res.Notices)
		}
		if restartErr != nil {
			return restartErr
		}
		slog.Info("sandbox restarted", "event", "sandbox.restart.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
				"name":   name,
				"action": "restarted",
			})
		}

		if opts.attach {
			return sb.Attach(ctx, cliutil.IOStreams())
		}

		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s restarted\nRun 'yoloai attach %s' to reconnect\n", name, name)
		return err
	})
}
