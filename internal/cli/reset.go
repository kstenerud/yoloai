package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
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

func newResetCmd() *cobra.Command {
	opts := &resetOpts{}
	cmd := &cobra.Command{
		Use:     "reset <name>",
		Short:   "Re-copy workdir into sandbox and reset diff baseline",
		GroupID: groupLifecycle,
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
	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}
	defer openCLIJSONLSink(name, cmd)()

	// --clear-state and --attach imply --restart
	if opts.clearState || opts.attach {
		opts.restart = true
	}

	if jsonEnabled(cmd) && opts.attach {
		return sandbox.NewUsageError("--json and --attach are incompatible")
	}

	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		return resetInRuntime(cmd, ctx, rt, name, opts)
	})
}

// resetInRuntime performs the sandbox reset and optional attach inside the runtime context.
func resetInRuntime(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string, opts *resetOpts) error {
	mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
	slog.Info("resetting sandbox", "event", "sandbox.reset", "sandbox", name, "restart", opts.restart, "clear_state", opts.clearState) //nolint:gosec // G706: name is validated by ValidateName
	if err := mgr.Reset(ctx, sandbox.ResetOptions{
		Name:       name,
		Restart:    opts.restart,
		ClearState: opts.clearState,
		KeepCache:  opts.keepCache,
		KeepFiles:  opts.keepFiles,
		NoPrompt:   opts.noPrompt,
		Debug:      opts.debug,
	}); err != nil {
		return sandboxErrorHint(name, err)
	}
	slog.Info("sandbox reset complete", "event", "sandbox.reset.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

	if jsonEnabled(cmd) {
		return writeJSON(cmd.OutOrStdout(), map[string]string{
			"name":   name,
			"action": "reset",
		})
	}

	if opts.attach {
		return attachAfterReset(cmd, ctx, rt, name)
	}

	if opts.restart {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\nRun 'yoloai attach %s' to reconnect\n", name, name)
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\n", name)
	return err
}

// attachAfterReset waits for tmux and attaches to the sandbox after a reset.
func attachAfterReset(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string) error {
	meta, err := sandbox.LoadMeta(sandbox.Dir(name))
	if err != nil {
		return err
	}
	user := tmuxExecUser(meta)
	containerName := sandbox.InstanceName(name)
	if err := waitForTmux(ctx, rt, containerName, name, 300*time.Second, user); err != nil {
		return fmt.Errorf("waiting for tmux session: %w", err)
	}
	return attachToSandbox(ctx, rt, containerName, name, user)
}
