// ABOUTME: Cobra "start" command: restarts a stopped sandbox with optional new
// ABOUTME: prompt, resume preamble, and auto-attach after the container comes up.
package cli

import (
	"context"
	"fmt"
	"log/slog"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

type startOpts struct {
	attach       bool
	resume       bool
	prompt       string
	promptFile   string
	vscodeTunnel bool
}

func newStartCmd() *cobra.Command {
	opts := &startOpts{}
	cmd := &cobra.Command{
		Use:     "start <name>",
		Short:   "Start a stopped sandbox",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runStart(cmd, args, opts) },
	}

	cmd.Flags().BoolVarP(&opts.attach, "attach", "a", false, "Auto-attach after starting")
	cmd.Flags().BoolVar(&opts.resume, "resume", false, "Re-feed original prompt with continuation preamble")
	cmd.Flags().StringVarP(&opts.prompt, "prompt", "p", "", "New prompt text (overwrites existing prompt)")
	cmd.Flags().StringVarP(&opts.promptFile, "prompt-file", "f", "", "File containing new prompt")
	cmd.Flags().BoolVar(&opts.vscodeTunnel, "vscode-tunnel", false, "Enable VS Code Remote Tunnel (persisted; takes effect on container recreate)")

	cmd.MarkFlagsMutuallyExclusive("resume", "prompt")
	cmd.MarkFlagsMutuallyExclusive("resume", "prompt-file")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")

	return cmd
}

// runStart implements the start command body.
func runStart(cmd *cobra.Command, args []string, opts *startOpts) error {
	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}
	defer openCLIJSONLSink(name, cmd)()

	if jsonEnabled(cmd) && opts.attach {
		return sandbox.NewUsageError("--json and --attach are incompatible")
	}

	if opts.attach {
		setTerminalTitle(name)
		defer setTerminalTitle("")
	}

	slog.Info("starting sandbox", "event", "sandbox.start", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	backend := resolveBackendForSandbox(name)
	return withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		if err := c.Start(ctx, name, sandbox.StartOptions{
			Resume:       opts.resume,
			Prompt:       opts.prompt,
			PromptFile:   opts.promptFile,
			VscodeTunnel: opts.vscodeTunnel,
		}); err != nil {
			return sandboxErrorHint(name, err)
		}
		slog.Info("sandbox started", "event", "sandbox.start.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), map[string]string{
				"name":   name,
				"action": "started",
			})
		}

		if opts.attach {
			return c.Attach(ctx, name, cliIOStreams())
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s started\nRun 'yoloai attach %s' to reconnect\n", name, name)
		return err
	})
}
