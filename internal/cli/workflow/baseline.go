// ABOUTME: Implements the `yoloai baseline` command with `advance`, `set`, and
// ABOUTME: `log` subcommands for manually correcting stuck sandbox baselines
// ABOUTME: after stash-pop conflicts or non-contiguous selective applies.
package workflow

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/spf13/cobra"
)

func NewBaselineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "baseline <subcommand>",
		Short:   "Manage sandbox baseline SHA",
		GroupID: cliutil.GroupWorkflow,
	}
	cmd.AddCommand(newBaselineAdvanceCmd(), newBaselineSetCmd(), newBaselineLogCmd())
	return cmd
}

// newBaselineAdvanceCmd implements `yoloai baseline advance <name>`.
// It moves the baseline to the current HEAD of the sandbox work copy.
func newBaselineAdvanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "advance <name>",
		Short: "Move baseline to current HEAD of the sandbox work copy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return withBaselineSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox, expected string) error {
				change, err := sb.Workdir().AdvanceBaseline(ctx, expected)
				if err != nil {
					return err
				}
				return printBaselineChange(cmd, name, expected, change)
			})
		},
	}
}

// newBaselineSetCmd implements `yoloai baseline set <name> <sha>`.
// It moves the baseline to the given commit (short SHA accepted).
func newBaselineSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> <sha>",
		Short: "Move baseline to a specific commit SHA",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, ref := args[0], args[1]
			return withBaselineSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox, expected string) error {
				change, err := sb.Workdir().SetBaseline(ctx, expected, ref)
				if err != nil {
					return err
				}
				return printBaselineChange(cmd, name, expected, change)
			})
		},
	}
}

// withBaselineSandbox reads the sandbox's current baseline (the compare-and-swap
// token the mutating verbs check against) and opens a client for the sandbox's
// backend, then runs fn with the resolved handle and that token. The CLI is a
// single optimistic actor: it passes the value it just read as the expected
// baseline, so the verb fails loudly with a *BaselineConflictError only if
// something moved the baseline between this read and the locked swap.
func withBaselineSandbox(cmd *cobra.Command, name string, fn func(ctx context.Context, sb *yoloai.Sandbox, expected string) error) error {
	env, err := cliutil.SandboxMetadata(cmd, name)
	if err != nil {
		return err
	}
	expected := env.Workdir().BaselineSHA

	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		return fn(ctx, sb, expected)
	})
}

// printBaselineChange prints the confirmation line for an advance/set:
// "Baseline advanced to <short-sha> (<subject>)". When oldSHA is non-empty it
// also prints a "Previous baseline" undo hint pointing at the value the caller
// swapped away from.
func printBaselineChange(cmd *cobra.Command, name, oldSHA string, change *yoloai.BaselineChange) error {
	w := cmd.OutOrStdout()
	if change.Subject != "" {
		if _, err := fmt.Fprintf(w, "Baseline advanced to %s (%s)\n", short8(change.NewSHA), change.Subject); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(w, "Baseline advanced to %s\n", short8(change.NewSHA)); err != nil {
		return err
	}
	if oldSHA != "" {
		old := short8(oldSHA)
		_, err := fmt.Fprintf(w, "Previous baseline: %s  — to undo: yoloai baseline set %s %s\n", old, name, old)
		return err
	}
	return nil
}

// newBaselineLogCmd implements `yoloai baseline log <name>`.
// It prints the sandbox work copy's git log from the sandbox inception commit
// to HEAD, marking the current baseline. This bounds the output to the sandbox
// session regardless of where the current baseline sits, so the log remains
// useful for recovery even after an accidental baseline advance.
func newBaselineLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log <name>",
		Short: "Show commit log of the sandbox work copy, marking the current baseline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return cliutil.WithWorkdir(cmd, name, func(ctx context.Context, wd *yoloai.Workdir) error {
				entries, err := wd.BaselineLog(ctx)
				if err != nil {
					return cliutil.SandboxErrorHint(name, err)
				}
				w := cmd.OutOrStdout()
				for _, e := range entries {
					marker := ""
					if e.IsBaseline {
						marker = "  ← baseline"
					}
					if _, err := fmt.Fprintf(w, "%s  %s%s\n", short8(e.SHA), e.Subject, marker); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

// short8 truncates a SHA to its first 8 characters for display.
func short8(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
