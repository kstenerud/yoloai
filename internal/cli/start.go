// ABOUTME: Cobra "start" command: restarts a stopped sandbox with optional new
// ABOUTME: prompt, resume preamble, and auto-attach after the container comes up.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/store"
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

	// Set terminal title early so it shows the sandbox name during start
	if opts.attach {
		setTerminalTitle(name)
		defer setTerminalTitle("")
	}

	slog.Info("starting sandbox", "event", "sandbox.start", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		return startInRuntime(cmd, ctx, rt, name, opts)
	})
}

// startInRuntime performs the sandbox start and optional attach inside the runtime context.
func startInRuntime(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string, opts *startOpts) error {
	mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr(), sandbox.WithLayout(cliLayout()))
	if err := mgr.Start(ctx, name, sandbox.StartOptions{
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

	if !opts.attach {
		return nil
	}

	return attachAfterStart(cmd, ctx, rt, name)
}

// attachAfterStart waits for tmux and attaches to the sandbox after a start.
func attachAfterStart(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string) error {
	meta, err := store.LoadMeta(cliLayout().SandboxDir(name))
	if err != nil {
		return err
	}
	user := tmuxExecUser(meta)
	containerName := store.InstanceName(name)
	if err := waitForTmux(ctx, rt, containerName, name, 300*time.Second, user); err != nil {
		return fmt.Errorf("waiting for tmux session: %w", err)
	}
	return attachToSandbox(ctx, rt, containerName, name, user)
}
