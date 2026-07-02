// ABOUTME: 'apply' command entry — wires CLI flags to the chosen apply
// ABOUTME: workflow (format-patch, no-commit, selective, export) and
// ABOUTME: holds shared helpers (arg parsing, tag transfer, result type).
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
func listSandboxTags(cmd *cobra.Command, name, hostPath string, unappliedOnly bool) []yoloai.TagInfo {
	var tags []yoloai.TagInfo
	//nolint:errcheck // best-effort: tag listing failure must not fail the apply
	_ = cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
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
		Use:   "apply <name> [<dir> | --all] [<ref>...] [-- <path>...]",
		Short: "Apply agent changes back to original work directory",
		Long: `Apply agent changes back to the original directory.

By default, only committed changes are applied (individual commits via
git format-patch/am). Uncommitted edits the agent left behind are
detected and reported but NOT applied; pass --include-uncommitted to also
bring them across as unstaged modifications.

<dir> is required when the sandbox tracks 2+ directories; it may be a
basename, a path suffix, an exact host path, or an exact mount path.

Specific commits can be cherry-picked by providing ref arguments:
  yoloai apply mybox abc123 def456       # specific commits
  yoloai apply mybox abc123..def456      # range
  yoloai apply mybox                     # all commits (default)

Use --no-commit to land the changes as a single unstaged patch in the
working tree instead of replaying the commits (combine with --include-uncommitted
to include uncommitted edits too). It's also used automatically when the
target isn't a git repository. Use --patches to export .patch files
without applying them.

Examples:
  yoloai apply mybox --all              # apply all tracked dirs`,
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
	cmd.Flags().Bool("all", false, "operate on all tracked directories")

	cmd.MarkFlagsMutuallyExclusive("no-commit", "patches")
	cmd.MarkFlagsMutuallyExclusive("no-commit", "tags")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "patches")

	return cmd
}

// applyFlags bundles the parsed CLI flags for the apply command.
type applyFlags struct {
	yes                bool
	noCommit           bool
	patchesDir         string
	includeUncommitted bool
	dryRun             bool
	withTags           bool
}

func runApplyCmd(cmd *cobra.Command, args []string) error {
	name, rest, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()

	flags, err := parseApplyFlags(cmd)
	if err != nil {
		return err
	}

	allFlag, _ := cmd.Flags().GetBool("all")

	if allFlag {
		if flags.patchesDir != "" {
			return yoerrors.NewUsageError("--patches cannot be used with --all; use a single dir specifier")
		}
		if flags.withTags {
			return yoerrors.NewUsageError("--tags with --all is not supported; use a single dir specifier")
		}
		return applyAll(cmd, name, flags)
	}

	// Load the sandbox read-model early so SelectTrackedDir can consume the
	// dir specifier before parseApplyArgs sees the remaining positionals.
	env, err := cliutil.SandboxMetadata(cmd, name)
	if err != nil {
		return err
	}
	// argsConsumedBeforeRest tracks how many positional args from the original
	// args slice have been consumed before rest: 1 for name, +1 if SelectTrackedDir
	// consumed a dir specifier. parseApplyArgs needs this to adjust ArgsLenAtDash.
	argsConsumedBeforeRest := 1

	hostPath, selectedDir, rest, err := cliutil.SelectTrackedDir(env, rest)
	if err != nil {
		return err
	}
	if hostPath != "" {
		argsConsumedBeforeRest = 2
	}

	// Parse refs and paths from remaining args (after name + optional dir specifier).
	refs, paths := parseApplyArgs(rest, cmd, argsConsumedBeforeRest)

	return dispatchApply(cmd, name, hostPath, selectedDir, refs, paths, flags)
}

// parseApplyFlags reads and validates flag values from the cobra command.
func parseApplyFlags(cmd *cobra.Command) (applyFlags, error) {
	var f applyFlags
	f.yes = cliutil.EffectiveYes(cmd)
	f.noCommit, _ = cmd.Flags().GetBool("no-commit")
	f.patchesDir, _ = cmd.Flags().GetString("patches")
	if f.patchesDir != "" {
		var err error
		f.patchesDir, err = cliutil.ExpandPath(f.patchesDir, cliutil.Layout().HomeDir, cliutil.Layout().Env().EnvForConfigInterpolation())
		if err != nil {
			return applyFlags{}, fmt.Errorf("expand patches path: %w", err)
		}
	}
	f.includeUncommitted, _ = cmd.Flags().GetBool("include-uncommitted")
	f.dryRun, _ = cmd.Flags().GetBool("dry-run")
	f.withTags, _ = cmd.Flags().GetBool("tags")
	return f, nil
}

// dispatchApply validates options and routes to the correct apply workflow.
func dispatchApply(cmd *cobra.Command, name, hostPath string, selectedDir yoloai.DirInfo, refs, paths []string, flags applyFlags) error {
	targetDir := selectedDir.HostPath

	// Validate mutually exclusive options
	if len(refs) > 0 && flags.noCommit {
		return yoerrors.NewUsageError("--no-commit cannot be used with commit refs — they are mutually exclusive")
	}
	if selectedDir.Mode == yoloai.DirModeRW {
		return yoerrors.NewUsageError("apply is not needed for :rw directories — changes are already live")
	}

	// --patches: export patch files instead of applying (handles all mount modes
	// and ref subsets via Workdir().Export). Dispatched before the apply paths.
	if flags.patchesDir != "" {
		return runExport(cmd, name, hostPath, selectedDir, refs, paths, flags.patchesDir, flags.includeUncommitted)
	}

	slog.Info("applying changes", "event", "sandbox.apply", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

	if !cliutil.JSONEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "Target: %s\n\n", targetDir) //nolint:errcheck
	}

	// Best-effort agent-running warning
	if !cliutil.JSONEnabled(cmd) {
		agentRunningWarning(cmd, name)
	}

	// Selective apply: specific commit refs
	if len(refs) > 0 {
		return applySelectedCommits(cmd, name, hostPath, targetDir, refs, paths, flags.yes, flags.dryRun, flags.withTags)
	}

	// --no-commit: land one unstaged patch (commits only unless --include-uncommitted).
	if flags.noCommit {
		return applyNoCommit(cmd, name, hostPath, targetDir, paths, flags.yes, flags.dryRun, flags.includeUncommitted)
	}

	return runApplyFormatPatch(cmd, name, hostPath, targetDir, paths, flags.yes, flags.dryRun, flags.includeUncommitted, flags.withTags)
}

// parseApplyArgs separates ref arguments from path arguments.
// Refs appear between the sandbox name (and optional dir specifier) and "--";
// paths appear after "--". Without "--", all remaining args are treated as refs
// if they look like hex SHA prefixes or ranges, otherwise all are treated as paths.
// argsConsumedBeforeRest is the number of positional args already consumed from
// the original args slice (1 for name-only, 2 for name+dir-specifier).
func parseApplyArgs(rest []string, cmd *cobra.Command, argsConsumedBeforeRest int) (refs []string, paths []string) {
	if len(rest) == 0 {
		return nil, nil
	}

	dashAt := cmd.ArgsLenAtDash()
	if dashAt >= 0 {
		// Explicit "--" separator. Account for args already consumed.
		beforeDash := min(max(dashAt-argsConsumedBeforeRest, 0), len(rest))
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
func applyTags(cmd *cobra.Command, name, hostPath string, tags []yoloai.TagInfo, shaMap map[string]string, withTags bool) (applied, skipped int) {
	if !withTags || len(tags) == 0 {
		return 0, 0
	}
	result, err := transferTags(cmd, name, hostPath, tags, shaMap)
	if err != nil || result == nil {
		return 0, 0
	}
	return result.Applied, result.Skipped
}

// targetIsGitRepo reports whether the sandbox's selected host work directory is
// a git repository — the apply target. Opens a client to query the library.
func targetIsGitRepo(cmd *cobra.Command, name, hostPath string, backend yoloai.BackendType) (bool, error) {
	var isGit bool
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		wd, wdErr := trackedDirHandle(sb, hostPath)
		if wdErr != nil {
			return wdErr
		}
		var checkErr error
		isGit, checkErr = wd.TargetIsGitRepo(ctx)
		return checkErr
	})
	return isGit, err
}

// transferTags re-creates the sandbox's tags on the host through
// Workdir().TransferTags and prints the per-tag outcomes. An empty shaMap makes
// the library match commits by metadata (the no-commits-applied path). Returns
// the result so callers can surface counts; the error is fatal only for the
// matching path (a provided map never matches).
func transferTags(cmd *cobra.Command, name, hostPath string, tags []yoloai.TagInfo, shaMap map[string]string) (*yoloai.TagTransferResult, error) {
	var result *yoloai.TagTransferResult
	err := cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
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

// applyAll applies changes across all tracked directories.
// One dir's failure does not abort the others.
// Returns an error if any dir failed.
func applyAll(cmd *cobra.Command, name string, flags applyFlags) error {
	env, err := cliutil.SandboxMetadata(cmd, name)
	if err != nil {
		return err
	}
	tracked := env.TrackedDirs()
	if len(tracked) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No tracked directories") //nolint:errcheck
		return nil
	}

	if !flags.yes && !flags.dryRun {
		confirmed, promptErr := cliutil.Confirm(cmd.Context(), fmt.Sprintf("Apply changes to all %d tracked directories? [y/N] ", len(tracked)), os.Stdin, cmd.ErrOrStderr())
		if promptErr != nil {
			return promptErr
		}
		if !confirmed {
			return nil
		}
	}

	var anyFailed bool
	for _, d := range tracked {
		applyErr := applyOneDir(cmd, name, d, flags)
		label := filepath.Base(d.HostPath)
		if applyErr != nil {
			anyFailed = true
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s  FAILED (%s)\n", label, applyErr) //nolint:errcheck
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s  done  -> %s\n", label, d.HostPath) //nolint:errcheck
		}
	}
	if anyFailed {
		return fmt.Errorf("one or more directories failed to apply")
	}
	return nil
}

// applyOneDir applies changes for a single tracked directory during --all.
func applyOneDir(cmd *cobra.Command, name string, d yoloai.DirInfo, flags applyFlags) error {
	// Determine mode: commits or no-commit
	mode := yoloai.ApplyModeCommits
	if flags.noCommit {
		mode = yoloai.ApplyModeNoCommit
	} else {
		// Check if target is git — if not, fall back to no-commit
		backend := cliutil.ResolveBackendForSandbox(name)
		isGit, checkErr := targetIsGitRepo(cmd, name, d.HostPath, backend)
		if checkErr == nil && !isGit {
			mode = yoloai.ApplyModeNoCommit
		}
	}

	return cliutil.WithTrackedDir(cmd, name, d.HostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		_, applyErr := wd.Apply(ctx, yoloai.WorkdirApplyOptions{
			Mode:               mode,
			IncludeUncommitted: flags.includeUncommitted,
			DryRun:             flags.dryRun,
		})
		return applyErr
	})
}
