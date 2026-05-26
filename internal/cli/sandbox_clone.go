package cli

// ABOUTME: `yoloai clone` — top-level command to clone a sandbox.
// ABOUTME: Also available as `yoloai sandbox clone` for backward compatibility.

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	yoloai "github.com/kstenerud/yoloai"
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
		return sandbox.NewUsageError("--json and --attach are incompatible")
	}

	// Courtesy free-space check before duplicating the workdir copy +
	// overlay. Clone is the heaviest allocate-per-sandbox op aside
	// from new. Swallow stat errors and non-blocking.
	if !jsonEnabled(cmd) {
		warnIfLowDisk(cmd.ErrOrStderr(), cliLayout().SandboxesDir())
	}

	// Set terminal title early so it shows the sandbox name during clone+start
	if attach && !noStart {
		setTerminalTitle(dst)
		defer setTerminalTitle("")
	}

	// Force-destroy existing destination before cloning. The existing dst's
	// backend may differ from src's, so this opens its own Client tied to
	// dst's current backend.
	if force {
		if _, err := os.Stat(cliLayout().SandboxDir(dst)); err == nil { //nolint:gosec // G703: dst is validated sandbox name
			destBackend := resolveBackendForSandbox(dst)
			if err := withClient(cmd, destBackend, func(ctx context.Context, c *yoloai.Client) error {
				return c.Destroy(ctx, dst, true)
			}); err != nil {
				return fmt.Errorf("destroy existing destination: %w", err)
			}
		}
	}

	// Source's backend governs the rest of the flow: after clone, dst inherits
	// src's backend (copied via meta.json), so Start needs the same backend.
	backend := resolveBackendForSandbox(src)
	return withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		slog.Info("cloning sandbox", "event", "sandbox.clone", "source", src, "dest", dst) //nolint:gosec // G706: src/dst are validated sandbox names
		if err := c.Clone(ctx, sandbox.CloneOptions{Source: src, Dest: dst}); err != nil {
			return err
		}
		slog.Info("clone complete", "event", "sandbox.clone.complete", "source", src, "dest", dst) //nolint:gosec // G706: src/dst are validated sandbox names

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

		return runCloneStart(cmd, ctx, c, src, dst, prompt, promptFile, attach)
	})
}

// runCloneStart starts the cloned sandbox and optionally attaches.
// Attach reaches for raw runtime via attachToSandboxByName — Client doesn't
// yet expose attach (see CONVENTIONS.md "Hybrid handlers").
func runCloneStart(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, src, dst, prompt, promptFile string, attach bool) error {
	if err := c.Start(ctx, dst, sandbox.StartOptions{
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
	return attachToSandboxByName(cmd, dst)
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
