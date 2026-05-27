// ABOUTME: 'apply' command entry — wires CLI flags to the chosen apply
// ABOUTME: workflow (format-patch, squash, selective, export, overlay) and
// ABOUTME: holds shared helpers (arg parsing, tag transfer, result type).
package cli

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// applyResult holds JSON output for the apply command.
type applyResult struct {
	Target         string `json:"target"`
	CommitsApplied int    `json:"commits_applied"`
	WIPApplied     bool   `json:"wip_applied"`
	TagsApplied    int    `json:"tags_applied"`
	TagsSkipped    int    `json:"tags_skipped"`
	Method         string `json:"method"` // "format-patch", "squash", "selective", "patches-export"
}

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply <name> [<ref>...] [-- <path>...]",
		Short: "Apply agent changes back to original work directory",
		Long: `Apply agent changes back to the original directory.

By default, only committed changes are applied (individual commits via
git format-patch/am). Uncommitted (WIP) edits the agent left behind are
detected and reported but NOT applied; pass --include-wip to also bring
them across as unstaged modifications.

Specific commits can be cherry-picked by providing ref arguments:
  yoloai apply mybox abc123 def456       # specific commits
  yoloai apply mybox abc123..def456      # range
  yoloai apply mybox                     # all commits (default)

Use --squash to flatten the committed changes into a single unstaged
patch (combine with --include-wip to include uncommitted edits too).
Use --patches to export .patch files without applying them.`,
		GroupID: cliutil.GroupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE:    runApplyCmd,
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("squash", false, "Flatten changes into a single unstaged patch")
	cmd.Flags().String("patches", "", "Export .patch files to directory instead of applying")
	cmd.Flags().Bool("include-wip", false, "Also apply uncommitted (work-in-progress) changes; default is commits only")
	cmd.Flags().Bool("dry-run", false, "Show what would be applied without applying")
	cmd.Flags().Bool("tags", false, "Transfer git tags created by the agent")

	cmd.MarkFlagsMutuallyExclusive("squash", "patches")
	cmd.MarkFlagsMutuallyExclusive("squash", "tags")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "patches")

	return cmd
}

func runApplyCmd(cmd *cobra.Command, args []string) error {
	name, rest, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()

	yes := cliutil.EffectiveYes(cmd)
	squash, _ := cmd.Flags().GetBool("squash")
	patchesDir, _ := cmd.Flags().GetString("patches")
	if patchesDir != "" {
		var expandErr error
		patchesDir, expandErr = sandbox.ExpandPath(patchesDir, filepath.Dir(cliutil.Layout().DataDir))
		if expandErr != nil {
			return fmt.Errorf("expand patches path: %w", expandErr)
		}
	}
	includeWIP, _ := cmd.Flags().GetBool("include-wip")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	withTags, _ := cmd.Flags().GetBool("tags")

	// Parse refs and paths from remaining args
	refs, paths := parseApplyArgs(rest, cmd)

	// Validate mutually exclusive options
	if len(refs) > 0 && squash {
		return sandbox.NewUsageError("--squash cannot be used with commit refs — they are mutually exclusive")
	}
	// Load metadata for target directory and mode validation
	meta, err := store.LoadMeta(cliutil.Layout().SandboxDir(name))
	if err != nil {
		return cliutil.SandboxErrorHint(name, err)
	}
	if meta.Workdir.Mode == "rw" {
		return sandbox.NewUsageError("apply is not needed for :rw directories — changes are already live")
	}

	slog.Info("applying changes", "event", "sandbox.apply", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	if hasOverlayDirs(meta) {
		return applyOverlay(cmd, name, meta, refs, paths, patchesDir, yes, dryRun)
	}

	if !cliutil.JSONEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "Target: %s\n\n", meta.Workdir.HostPath) //nolint:errcheck
	}

	// Best-effort agent-running warning
	if !cliutil.JSONEnabled(cmd) {
		agentRunningWarning(cmd, name)
	}

	// Selective apply: specific commit refs
	if len(refs) > 0 {
		return applySelectedCommits(cmd, name, refs, paths, meta, yes, dryRun, withTags)
	}

	// --squash: flatten into one unstaged patch (commits only unless --include-wip).
	if squash {
		return applySquash(cmd, name, paths, meta, yes, dryRun, includeWIP)
	}

	return runApplyFormatPatch(cmd, name, paths, meta, patchesDir, yes, dryRun, includeWIP, withTags)
}

// parseApplyArgs separates ref arguments from path arguments.
// Refs appear between the sandbox name and "--"; paths appear after "--".
// Without "--", all remaining args are treated as refs if they look like
// hex SHA prefixes or ranges, otherwise all are treated as paths.
func parseApplyArgs(rest []string, cmd *cobra.Command) (refs []string, paths []string) {
	if len(rest) == 0 {
		return nil, nil
	}

	dashAt := cmd.ArgsLenAtDash()
	if dashAt >= 0 {
		// Explicit "--" separator. Account for name already consumed.
		beforeDash := min(max(dashAt-1, 0), len(rest))
		refs = rest[:beforeDash]
		paths = rest[beforeDash:]
		return refs, paths
	}

	// No "--": check if all args look like refs
	allRefs := true
	for _, arg := range rest {
		if !looksLikeRef(arg) {
			allRefs = false
			break
		}
	}

	if allRefs {
		return rest, nil
	}

	// If the first arg doesn't look like a ref, they're all paths (backward compat)
	return nil, rest
}

// buildTagsByCommit builds a map of lowercase commit SHA → tag names from a tag list.
func buildTagsByCommit(tags []sandbox.TagInfo) map[string][]string {
	m := make(map[string][]string, len(tags))
	for _, t := range tags {
		key := strings.ToLower(t.SHA)
		m[key] = append(m[key], t.Name)
	}
	return m
}

// applyTags transfers tags to the host using the sandbox→host SHA map.
// sandboxWorkDir is used to fetch the full tag message (which is not stored
// in TagInfo to keep tag listing fast and reliable).
// Returns counts of applied and skipped tags. No-ops if withTags is false.
func applyTags(cmd *cobra.Command, tags []sandbox.TagInfo, shaMap map[string]string, sandboxWorkDir, targetDir string, withTags bool) (applied, skipped int) {
	if !withTags || len(tags) == 0 {
		return 0, 0
	}
	isJSON := cliutil.JSONEnabled(cmd)
	for _, tag := range tags {
		hostSHA, ok := shaMap[strings.ToLower(tag.SHA)]
		if !ok {
			skipped++
			if !isJSON {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: tag %q skipped (target commit not applied)\n", tag.Name) //nolint:errcheck
			}
			continue
		}
		// Fetch full tag message from sandbox
		message := sandbox.GetTagMessage(sandboxWorkDir, tag.Name)
		if createErr := workspace.CreateTag(targetDir, tag.Name, hostSHA, message); createErr != nil {
			skipped++
			if !isJSON {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: tag %q: %v\n", tag.Name, createErr) //nolint:errcheck
			}
		} else {
			applied++
			if !isJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "Tag %q applied\n", tag.Name) //nolint:errcheck
			}
		}
	}
	return applied, skipped
}
