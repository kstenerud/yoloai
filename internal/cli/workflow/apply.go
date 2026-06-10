// ABOUTME: 'apply' command entry — wires CLI flags to the chosen apply
// ABOUTME: workflow (format-patch, no-commit, selective, export, overlay) and
// ABOUTME: holds shared helpers (arg parsing, tag transfer, result type).
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// listSandboxTags fetches a sandbox's tags through the public Workdir handle.
// Best-effort: a failed fetch yields nil (tag annotations are decorative and
// never fail the apply). When unappliedOnly is true, only tags absent on the
// host are returned. The returned tags carry their annotated-tag Message.
func listSandboxTags(cmd *cobra.Command, name string, unappliedOnly bool) []yoloai.TagInfo {
	var tags []yoloai.TagInfo
	//nolint:errcheck // best-effort: tag listing failure must not fail the apply
	_ = cliutil.WithWorkdir(cmd, name, func(ctx context.Context, wd *yoloai.Workdir) error {
		tags, _ = wd.Tags(ctx, yoloai.WorkdirTagsOptions{UnappliedOnly: unappliedOnly})
		return nil
	})
	return tags
}

// applyResult holds JSON output for the apply command.
type applyResult struct {
	Target             string `json:"target"`
	CommitsApplied     int    `json:"commits_applied"`
	UncommittedApplied bool   `json:"uncommitted_applied"`
	TagsApplied        int    `json:"tags_applied"`
	TagsSkipped        int    `json:"tags_skipped"`
	Method             string `json:"method"` // "format-patch", "no-commit", "selective", "patches-export"
}

func NewApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply <name> [<ref>...] [-- <path>...]",
		Short: "Apply agent changes back to original work directory",
		Long: `Apply agent changes back to the original directory.

By default, only committed changes are applied (individual commits via
git format-patch/am). Uncommitted edits the agent left behind are
detected and reported but NOT applied; pass --include-uncommitted to also
bring them across as unstaged modifications.

Specific commits can be cherry-picked by providing ref arguments:
  yoloai apply mybox abc123 def456       # specific commits
  yoloai apply mybox abc123..def456      # range
  yoloai apply mybox                     # all commits (default)

Use --no-commit to land the changes as a single unstaged patch in the
working tree instead of replaying the commits (combine with --include-uncommitted
to include uncommitted edits too). It's also used automatically when the
target isn't a git repository. Use --patches to export .patch files
without applying them.`,
		GroupID: cliutil.GroupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE:    runApplyCmd,
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("no-commit", false, "Apply changes as a single unstaged patch instead of replaying commits")
	cmd.Flags().String("patches", "", "Export .patch files to directory instead of applying")
	cmd.Flags().Bool("include-uncommitted", false, "Also apply uncommitted changes; default is commits only")
	cmd.Flags().Bool("dry-run", false, "Show what would be applied without applying")
	cmd.Flags().Bool("tags", false, "Transfer git tags created by the agent")

	cmd.MarkFlagsMutuallyExclusive("no-commit", "patches")
	cmd.MarkFlagsMutuallyExclusive("no-commit", "tags")
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
	noCommit, _ := cmd.Flags().GetBool("no-commit")
	patchesDir, _ := cmd.Flags().GetString("patches")
	if patchesDir != "" {
		var expandErr error
		patchesDir, expandErr = cliutil.ExpandPath(patchesDir, cliutil.Layout().HomeDir, cliutil.Layout())
		if expandErr != nil {
			return fmt.Errorf("expand patches path: %w", expandErr)
		}
	}
	includeUncommitted, _ := cmd.Flags().GetBool("include-uncommitted")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	withTags, _ := cmd.Flags().GetBool("tags")

	// Parse refs and paths from remaining args
	refs, paths := parseApplyArgs(rest, cmd)

	// Validate mutually exclusive options
	if len(refs) > 0 && noCommit {
		return yoerrors.NewUsageError("--no-commit cannot be used with commit refs — they are mutually exclusive")
	}
	// Load the sandbox read-model for target directory and mode validation.
	env, err := cliutil.SandboxMetadata(cmd, name)
	if err != nil {
		return err
	}
	if env.Workdir.Mode == yoloai.DirModeRW {
		return yoerrors.NewUsageError("apply is not needed for :rw directories — changes are already live")
	}

	// --patches: export patch files instead of applying (handles all mount modes
	// and ref subsets via Workdir().Export). Dispatched before the apply paths.
	if patchesDir != "" {
		return runExport(cmd, name, env, refs, paths, patchesDir, includeUncommitted)
	}

	slog.Info("applying changes", "event", "sandbox.apply", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	if env.HasOverlayDirs() {
		return applyOverlay(cmd, name, env, refs, paths, yes, dryRun)
	}

	if !cliutil.JSONEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "Target: %s\n\n", env.Workdir.HostPath) //nolint:errcheck
	}

	// Best-effort agent-running warning
	if !cliutil.JSONEnabled(cmd) {
		agentRunningWarning(cmd, name)
	}

	// Selective apply: specific commit refs
	if len(refs) > 0 {
		return applySelectedCommits(cmd, name, refs, paths, env, yes, dryRun, withTags)
	}

	// --no-commit: land one unstaged patch (commits only unless --include-uncommitted).
	if noCommit {
		return applyNoCommit(cmd, name, paths, env, yes, dryRun, includeUncommitted)
	}

	return runApplyFormatPatch(cmd, name, paths, env, yes, dryRun, includeUncommitted, withTags)
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
func buildTagsByCommit(tags []yoloai.TagInfo) map[string][]string {
	m := make(map[string][]string, len(tags))
	for _, t := range tags {
		key := strings.ToLower(t.SHA)
		m[key] = append(m[key], t.Name)
	}
	return m
}

// applyTags transfers tags to the host via the library's TransferTags verb,
// using the sandbox→host SHA map. Returns counts of applied and skipped tags.
// No-ops if withTags is false. Best-effort: a transfer failure is swallowed (a
// provided SHA map can't trigger the matching path, so this won't error in
// practice) and reported as zero counts. For the matching path use
// transferTags directly so its error stays fatal.
func applyTags(cmd *cobra.Command, name string, tags []yoloai.TagInfo, shaMap map[string]string, withTags bool) (applied, skipped int) {
	if !withTags || len(tags) == 0 {
		return 0, 0
	}
	result, err := transferTags(cmd, name, tags, shaMap)
	if err != nil || result == nil {
		return 0, 0
	}
	return result.Applied, result.Skipped
}

// targetIsGitRepo reports whether the sandbox's host work directory is a git
// repository — the apply target. Opens a client to query the library.
func targetIsGitRepo(cmd *cobra.Command, name string, backend yoloai.BackendType) (bool, error) {
	var isGit bool
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		var checkErr error
		isGit, checkErr = sb.Workdir().TargetIsGitRepo(ctx)
		return checkErr
	})
	return isGit, err
}

// transferTags re-creates the sandbox's tags on the host through
// Workdir().TransferTags and prints the per-tag outcomes. An empty shaMap makes
// the library match commits by metadata (the no-commits-applied path). Returns
// the result so callers can surface counts; the error is fatal only for the
// matching path (a provided map never matches).
func transferTags(cmd *cobra.Command, name string, tags []yoloai.TagInfo, shaMap map[string]string) (*yoloai.TagTransferResult, error) {
	var result *yoloai.TagTransferResult
	err := cliutil.WithWorkdir(cmd, name, func(ctx context.Context, wd *yoloai.Workdir) error {
		var transferErr error
		result, transferErr = wd.TransferTags(ctx, yoloai.WorkdirTransferTagsOptions{Tags: tags, SHAMap: shaMap})
		return transferErr
	})
	if err != nil {
		return nil, err
	}
	printTagOutcomes(cmd, result)
	return result, nil
}

// printTagOutcomes renders the per-tag transfer results (human-mode only).
func printTagOutcomes(cmd *cobra.Command, result *yoloai.TagTransferResult) {
	if result == nil || cliutil.JSONEnabled(cmd) {
		return
	}
	for _, o := range result.Outcomes {
		switch {
		case o.Applied:
			fmt.Fprintf(cmd.OutOrStdout(), "Tag %q applied\n", o.Name) //nolint:errcheck
		case o.Unmatched:
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: tag %q skipped (target commit not applied)\n", o.Name) //nolint:errcheck
		default:
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: tag %q: %s\n", o.Name, o.Err) //nolint:errcheck
		}
	}
}
