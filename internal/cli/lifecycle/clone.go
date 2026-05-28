package lifecycle

// ABOUTME: `yoloai clone` — top-level command to clone a sandbox.
// ABOUTME: Also available as `yoloai sandbox clone` for backward compatibility.

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func runClone(cmd *cobra.Command, args []string) error {
	src, dst := args[0], args[1]
	force, _ := cmd.Flags().GetBool("force")
	noStart, _ := cmd.Flags().GetBool("no-start")
	attach, _ := cmd.Flags().GetBool("attach")
	prompt, _ := cmd.Flags().GetString("prompt")
	promptFile, _ := cmd.Flags().GetString("prompt-file")

	if cliutil.JSONEnabled(cmd) && attach {
		return sandbox.NewUsageError("--json and --attach are incompatible")
	}

	// Courtesy free-space check before duplicating the workdir copy +
	// overlay. Clone is the heaviest allocate-per-sandbox op aside
	// from new. Swallow stat errors and non-blocking.
	if !cliutil.JSONEnabled(cmd) {
		cliutil.WarnIfLowDisk(cmd.ErrOrStderr(), cliutil.Layout().SandboxesDir())
	}

	// Set terminal title early so it shows the sandbox name during clone+start
	if attach && !noStart {
		cliutil.SetTerminalTitle(dst)
		defer cliutil.SetTerminalTitle("")
	}

	// Force-destroy existing destination before cloning. The existing dst's
	// backend may differ from src's, so this opens its own Client tied to
	// dst's current backend.
	if force {
		if err := forceDestroyIfExists(cmd, dst); err != nil {
			return err
		}
	}

	// Source's backend governs the rest of the flow: after clone, dst inherits
	// src's backend (copied via meta.json), so Start needs the same backend.
	backend := cliutil.ResolveBackendForSandbox(src)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		slog.Info("cloning sandbox", "event", "sandbox.clone", "source", src, "dest", dst) //nolint:gosec // G706: src/dst are validated sandbox names
		if err := c.Clone(ctx, sandbox.CloneOptions{Source: src, Dest: dst}); err != nil {
			return err
		}
		slog.Info("clone complete", "event", "sandbox.clone.complete", "source", src, "dest", dst) //nolint:gosec // G706: src/dst are validated sandbox names

		if noStart {
			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
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
// Attach reaches for raw runtime via AttachToSandboxByName — Client doesn't
// yet expose attach (see CONVENTIONS.md "Hybrid handlers").
func runCloneStart(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, src, dst, prompt, promptFile string, attach bool) error {
	sb, err := c.Sandbox(dst)
	if err != nil {
		return err
	}
	// Start notices ("Sandbox Y started") are redundant with clone's own
	// "Cloned X → Y (started)" output below, so they're discarded here.
	if _, err := sb.Start(ctx, sandbox.StartOptions{
		Prompt:     prompt,
		PromptFile: promptFile,
	}); err != nil {
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
			"source": src,
			"dest":   dst,
			"action": "started",
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Cloned %s → %s (started)\n", src, dst) //nolint:errcheck

	if !attach {
		return nil
	}
	return cliutil.AttachToSandboxByName(cmd, dst)
}

// forceDestroyIfExists destroys the sandbox at dst if it already exists on disk.
// The existing dst's backend may differ from src's, so this opens its own Client.
func forceDestroyIfExists(cmd *cobra.Command, dst string) error {
	if _, statErr := os.Stat(cliutil.Layout().SandboxDir(dst)); os.IsNotExist(statErr) { //nolint:gosec // G703: dst is validated sandbox name
		return nil // does not exist — nothing to destroy
	}
	destBackend := cliutil.ResolveBackendForSandbox(dst)
	return cliutil.WithClient(cmd, destBackend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(dst)
		if err != nil {
			return err
		}
		_, destroyErr := sb.Destroy(ctx, yoloai.DestroyOptions{Force: true})
		return destroyErr
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

func NewCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "clone <source> <dest>",
		Short:   "Clone a sandbox",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ExactArgs(2),
		RunE:    runClone,
	}
	addCloneFlags(cmd)
	return cmd
}
