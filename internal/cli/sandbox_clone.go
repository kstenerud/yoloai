package cli

// ABOUTME: `yoloai clone` — top-level command to clone a sandbox.
// ABOUTME: Also available as `yoloai sandbox clone` for backward compatibility.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func runClone(cmd *cobra.Command, args []string) error {
	src, dst := args[0], args[1]
	force, _ := cmd.Flags().GetBool("force")
	noStart, _ := cmd.Flags().GetBool("no-start")
	attach, _ := cmd.Flags().GetBool("attach")
	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")

	if jsonEnabled(cmd) && attach {
		return fmt.Errorf("--json and --attach are incompatible")
	}

	// Set terminal title early so it shows the sandbox name during clone+start
	if attach && !noStart {
		setTerminalTitle(dst)
		defer setTerminalTitle("")
	}

	// Force-destroy existing destination before cloning.
	if force {
		if _, err := os.Stat(sandbox.Dir(dst)); err == nil {
			backend := resolveBackendForSandbox(dst)
			if err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
				return mgr.Destroy(ctx, dst)
			}); err != nil {
				return fmt.Errorf("destroy existing destination: %w", err)
			}
		}
	}

	// Clone (no runtime needed).
	slog.Info("cloning sandbox", "event", "sandbox.clone", "source", src, "dest", dst)
	mgr := sandbox.NewManager(nil, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
	if err := mgr.Clone(cmd.Context(), sandbox.CloneOptions{Source: src, Dest: dst}); err != nil {
		return err
	}
	slog.Info("clone complete", "event", "sandbox.clone.complete", "source", src, "dest", dst)

	if noStart {
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"source": src,
				"dest":   dst,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Cloned %s → %s\n", src, dst) //nolint:errcheck
		return nil
	}

	// Start (and optionally attach) — needs a runtime.
	backend := resolveBackendForSandbox(dst)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		startMgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		if err := startMgr.Start(ctx, dst, sandbox.StartOptions{
			Prompt:     prompt,
			PromptFile: promptFile,
		}); err != nil {
			return err
		}

		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"source": src,
				"dest":   dst,
				"action": "started",
			})
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Cloned %s → %s (started)\n", src, dst) //nolint:errcheck

		if !attach {
			return nil
		}

		meta, err := sandbox.LoadMeta(sandbox.Dir(dst))
		if err != nil {
			return err
		}
		user := tmuxExecUser(meta)
		containerName := sandbox.InstanceName(dst)
		if err := waitForTmux(ctx, rt, containerName, 30*time.Second, user); err != nil {
			return fmt.Errorf("waiting for tmux session: %w", err)
		}
		return attachToSandbox(ctx, rt, containerName, dst, user)
	})
}

// addCloneFlags registers the shared flags for clone commands.
func addCloneFlags(cmd *cobra.Command) {
	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after starting")
	cmd.Flags().StringP("prompt", "p", "", "New prompt text (overwrites existing prompt)")
	cmd.Flags().StringP("prompt-file", "f", "", "File containing new prompt")
	cmd.Flags().Bool("no-start", false, "Clone without starting")
	cmd.Flags().Bool("force", false, "Replace existing destination")

	cmd.MarkFlagsMutuallyExclusive("no-start", "attach")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")
}

func newCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "clone <source> <dest>",
		Short:   "Clone a sandbox",
		GroupID: groupLifecycle,
		Args:    cobra.ExactArgs(2),
		RunE:    runClone,
	}
	addCloneFlags(cmd)
	return cmd
}
