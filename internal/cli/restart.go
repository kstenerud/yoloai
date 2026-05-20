// ABOUTME: CLI command to restart a sandbox (stop + start).
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

type restartOpts struct {
	attach       bool
	resume       bool
	prompt       string
	promptFile   string
	isolation    string
	vscodeTunnel bool
}

func newRestartCmd() *cobra.Command {
	opts := &restartOpts{}
	cmd := &cobra.Command{
		Use:     "restart <name>",
		Short:   "Restart the agent in an existing sandbox",
		GroupID: groupLifecycle,
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
	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}
	defer openCLIJSONLSink(name, cmd)()

	if jsonEnabled(cmd) && opts.attach {
		return sandbox.NewUsageError("--json and --attach are incompatible")
	}

	// Set terminal title early so it shows the sandbox name during restart
	if opts.attach {
		setTerminalTitle(name)
		defer setTerminalTitle("")
	}

	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		return restartInRuntime(cmd, ctx, rt, name, opts)
	})
}

// restartInRuntime performs stop/start and optional attach inside the runtime context.
func restartInRuntime(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string, opts *restartOpts) error {
	mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())

	slog.Info("restarting sandbox", "event", "sandbox.restart", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	if err := mgr.Stop(ctx, name); err != nil {
		return err
	}
	if err := mgr.Start(ctx, name, sandbox.StartOptions{
		Resume:       opts.resume,
		Prompt:       opts.prompt,
		PromptFile:   opts.promptFile,
		Isolation:    opts.isolation,
		VscodeTunnel: opts.vscodeTunnel,
	}); err != nil {
		return err
	}
	slog.Info("sandbox restarted", "event", "sandbox.restart.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

	if jsonEnabled(cmd) {
		return writeJSON(cmd.OutOrStdout(), map[string]string{
			"name":   name,
			"action": "restarted",
		})
	}

	if opts.attach {
		return attachAfterRestart(cmd, ctx, rt, name)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s restarted\nRun 'yoloai attach %s' to reconnect\n", name, name)
	return err
}

// attachAfterRestart waits for tmux and attaches to the sandbox after a restart.
func attachAfterRestart(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string) error {
	meta, err := store.LoadMeta(store.Dir(name))
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
