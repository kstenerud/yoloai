// ABOUTME: Implements the `yoloai baseline` command with `advance`, `set`, and
// ABOUTME: `log` subcommands for manually correcting stuck sandbox baselines
// ABOUTME: after stash-pop conflicts or non-contiguous selective applies.
package cli

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

func newBaselineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "baseline <subcommand>",
		Short:   "Manage sandbox baseline SHA",
		GroupID: groupWorkflow,
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
			meta, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}

			sha, err := workspace.HeadSHA(workDir)
			if err != nil {
				return fmt.Errorf("resolve HEAD: %w", err)
			}
			return advanceBaselineAndPrint(cmd, name, meta.Workdir.BaselineSHA, sha, workDir)
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
			name, shortSHA := args[0], args[1]
			meta, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}

			// Resolve short SHA to full SHA via git.
			out, err := workspace.NewGitCmd(workDir, "rev-parse", shortSHA).Output()
			if err != nil {
				return fmt.Errorf("resolve sha %q: %w", shortSHA, err)
			}
			sha := strings.TrimSpace(string(out))

			return advanceBaselineAndPrint(cmd, name, meta.Workdir.BaselineSHA, sha, workDir)
		},
	}
}

// loadBaselineContext loads the sandbox metadata, validates that the workdir
// mode supports baseline management (:copy only), and returns the work copy
// path on the host. Returns a clear user-facing error for :rw and :overlay.
func loadBaselineContext(name string) (*store.Meta, string, error) {
	sandboxDir := cliutil.Layout().SandboxDir(name)
	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return nil, "", cliutil.SandboxErrorHint(name, err)
	}

	switch meta.Workdir.Mode {
	case "rw":
		return nil, "", sandbox.NewUsageError("baseline is not tracked for :rw directories")
	case "overlay":
		return nil, "", sandbox.NewUsageError("use git commands inside the container to manage overlay baselines")
	}

	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
	return meta, workDir, nil
}

// advanceBaselineAndPrint updates the baseline to sha and prints a
// confirmation line: "Baseline advanced to <short-sha> (<subject>)".
// If oldSHA is non-empty, it also prints a "Previous baseline" undo hint.
func advanceBaselineAndPrint(cmd *cobra.Command, name, oldSHA, sha, workDir string) error {
	if err := patch.AdvanceBaselineTo(cliutil.Layout(), name, sha); err != nil {
		return fmt.Errorf("update baseline: %w", err)
	}

	// Fetch the commit subject for the confirmation message.
	out, err := workspace.NewGitCmd(workDir, "log", "--format=%s", "-1", sha).Output()
	if err != nil {
		// Non-fatal: print without subject.
		_, werr := fmt.Fprintf(cmd.OutOrStdout(), "Baseline advanced to %.8s\n", sha)
		return werr
	}
	subject := strings.TrimSpace(string(out))
	shortSHA := sha
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "Baseline advanced to %s (%s)\n", shortSHA, subject); err != nil {
		return err
	}
	if oldSHA != "" {
		oldShort := oldSHA
		if len(oldShort) > 8 {
			oldShort = oldShort[:8]
		}
		_, werr := fmt.Fprintf(w, "Previous baseline: %s  — to undo: yoloai baseline set %s %s\n", oldShort, name, oldShort)
		return werr
	}
	return nil
}

// newBaselineLogCmd implements `yoloai baseline log <name>`.
// It prints the sandbox work copy's git log from the sandbox inception commit
// to HEAD, marking the current baseline. This bounds the output to the sandbox
// session regardless of where the current baseline sits, so the log remains
// useful for recovery even after an accidental baseline advance.
//
// Inception detection priority:
//  1. meta.Workdir.InceptionSHA (written at sandbox creation for all new sandboxes)
//  2. First commit authored by yoloai@localhost (legacy fresh-repo sandboxes)
//  3. Full log fallback (old sandboxes on existing repos with no inception marker)
func newBaselineLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log <name>",
		Short: "Show commit log of the sandbox work copy, marking the current baseline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			meta, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}

			baselineSHA := meta.Workdir.BaselineSHA
			w := cmd.OutOrStdout()

			// Determine the inception SHA using priority order.
			inceptionSHA := meta.Workdir.InceptionSHA
			if inceptionSHA == "" {
				// Legacy: find the first commit authored by yoloai@localhost.
				inceptionOut, gitErr := workspace.NewGitCmd(workDir,
					"log", "--format=%H", "--author=yoloai@localhost",
					"--reverse", "--max-count=1",
				).Output()
				if gitErr == nil {
					inceptionSHA = strings.TrimSpace(string(inceptionOut))
				}
			}

			if inceptionSHA == "" {
				// No inception marker found — fall back to full log.
				out, logErr := workspace.NewGitCmd(workDir, "log", "--format=%H %s").Output()
				if logErr != nil {
					return fmt.Errorf("git log: %w", logErr)
				}
				return printLogLines(w, string(out), baselineSHA)
			}

			// Show commits after inception (agent work), then the inception commit.
			out, err := workspace.NewGitCmd(workDir, "log", "--format=%H %s", inceptionSHA+"..HEAD").Output()
			if err != nil {
				return fmt.Errorf("git log: %w", err)
			}
			if err := printLogLines(w, string(out), baselineSHA); err != nil {
				return err
			}
			inceptionLine, err := workspace.NewGitCmd(workDir, "log", "--format=%H %s", "-1", inceptionSHA).Output()
			if err != nil {
				return fmt.Errorf("git log inception: %w", err)
			}
			return printLogLines(w, string(inceptionLine), baselineSHA)
		},
	}
}

// printLogLines prints git log output (one "%H %s" line per commit), marking
// the commit whose full SHA matches markerSHA with "  ← baseline".
func printLogLines(w interface{ Write([]byte) (int, error) }, output, markerSHA string) error {
	for line := range strings.SplitSeq(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		before, after, ok := strings.Cut(line, " ")
		var fullSHA, subject string
		if !ok {
			fullSHA, subject = line, ""
		} else {
			fullSHA, subject = before, after
		}
		shortSHA := fullSHA
		if len(shortSHA) > 8 {
			shortSHA = shortSHA[:8]
		}
		marker := ""
		if markerSHA != "" && fullSHA == markerSHA {
			marker = "  ← baseline"
		}
		if _, err := fmt.Fprintf(w, "%s  %s%s\n", shortSHA, subject, marker); err != nil {
			return err
		}
	}
	return nil
}
