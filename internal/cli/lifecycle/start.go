// ABOUTME: Cobra "start" command: restarts a stopped sandbox with optional new
// ABOUTME: prompt, resume preamble, and auto-attach after the container comes up.
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

type startOpts struct {
	attach       bool
	resume       bool
	prompt       string
	promptFile   string
	vscodeTunnel bool
	env          []string
	broker       bool
	noBroker     bool
}

func NewStartCmd() *cobra.Command {
	opts := &startOpts{}
	cmd := &cobra.Command{
		Use:     "start <name>",
		Short:   "Start a stopped sandbox",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runStart(cmd, args, opts) },
	}

	cmd.Flags().BoolVarP(&opts.attach, "attach", "a", false, "Auto-attach after starting")
	cmd.Flags().BoolVar(&opts.resume, "resume", false, "Re-feed original prompt with continuation preamble")
	cmd.Flags().StringVarP(&opts.prompt, "prompt", "p", "", "New prompt text (overwrites existing prompt)")
	cmd.Flags().StringVarP(&opts.promptFile, "prompt-file", "f", "", "File containing new prompt")
	cmd.Flags().BoolVar(&opts.vscodeTunnel, "vscode-tunnel", false, "Enable VS Code Remote Tunnel (persisted; takes effect on container recreate)")
	cmd.Flags().StringArrayVar(&opts.env, "env", nil, "Per-sandbox env var KEY=VAL (not persisted; re-supply on each start)")
	// INTERIM SHAPE — do not copy this as a pattern (DF225). One tri-state
	// (auto / required / disabled) carried as two booleans whose fourth
	// combination is meaningless and is excluded at runtime below rather than by
	// the type. It is deliberately identical to `new`'s: the same encoding
	// already spans the public options struct, state.State, and the persisted
	// meta, so introducing the right shape *here* would make five layers
	// disagree, which is worse than six that agree. Collapsing it changes the
	// on-disk field names, so it waits for v0.13.0, which is migration-bearing
	// anyway.
	cmd.Flags().BoolVar(&opts.broker, "broker", false, "Require credential brokering from this start on: keep the agent's API key host-side (persisted)")
	cmd.Flags().BoolVar(&opts.noBroker, "no-broker", false, "Disable credential brokering from this start on: deliver the agent's API key into the sandbox directly (persisted)")

	cmd.MarkFlagsMutuallyExclusive("broker", "no-broker")
	cmd.MarkFlagsMutuallyExclusive("resume", "prompt")
	cmd.MarkFlagsMutuallyExclusive("resume", "prompt-file")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")

	return cmd
}

// runStart implements the start command body.
func runStart(cmd *cobra.Command, args []string, opts *startOpts) error {
	name, _, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()

	if cliutil.JSONEnabled(cmd) && opts.attach {
		return yoerrors.NewUsageError("--json and --attach are incompatible")
	}

	if opts.attach {
		cliutil.SetTerminalTitle(name)
		defer cliutil.SetTerminalTitle("")
	}

	envMap, err := parseEnvSlice(opts.env)
	if err != nil {
		return err
	}

	slog.Info("starting sandbox", "event", "sandbox.start", "sandbox", name)
	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		res, startErr := sb.Start(ctx, yoloai.SandboxStartOptions{
			Resume:       opts.resume,
			Prompt:       opts.prompt,
			PromptFile:   opts.promptFile,
			VscodeTunnel: opts.vscodeTunnel,
			Env:          envMap,
			Broker:       opts.broker,
			NoBroker:     opts.noBroker,
		})
		if res != nil {
			cliutil.RenderNotices(cmd, res.Notices)
		}
		if startErr != nil {
			return cliutil.SandboxErrorHint(name, startErr)
		}
		slog.Info("sandbox started", "event", "sandbox.start.complete", "sandbox", name)

		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
				"name":   name,
				"action": "started",
			})
		}

		if opts.attach {
			return cliutil.WithTerminal(func(io yoloai.IOStreams) error {
				return sb.Agent().Attach(ctx, io)
			})
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s started\nRun 'yoloai attach %s' to reconnect\n", name, name)
		return err
	})
}
