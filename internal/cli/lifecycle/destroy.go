// ABOUTME: Cobra "destroy" command: stops and removes one or more sandboxes,
// ABOUTME: with wildcard expansion, --all, and --abandon-unapplied active-work refusal.
package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// hasWildcard returns true if the string contains * or ? characters.
func hasWildcard(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// expandWildcard matches a wildcard pattern against all sandbox names.
// Returns matching sandbox names, or an error if no matches found.
func expandWildcard(ctx context.Context, c *yoloai.Client, pattern string) ([]string, error) {
	infos, err := c.ListSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	var matches []string
	for _, info := range infos {
		matched, err := filepath.Match(pattern, info.Environment.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		if matched {
			matches = append(matches, info.Environment.Name)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no sandboxes match pattern %q", pattern)
	}

	return matches, nil
}

func NewDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "destroy <name>...",
		Short:   "Stop and remove sandboxes",
		GroupID: cliutil.GroupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE:    runDestroyCmd,
	}

	cmd.Flags().Bool("all", false, "Destroy all sandboxes")
	cmd.Flags().Bool("abandon-unapplied", false, "Destroy even when a sandbox has unapplied changes")

	return cmd
}

func runDestroyCmd(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	abandonUnapplied, _ := cmd.Flags().GetBool("abandon-unapplied")

	if all && len(args) > 0 {
		return yoerrors.NewUsageError("cannot specify sandbox names with --all")
	}

	// Resolve backend: from first named sandbox, or config default for --all/wildcards
	backend, warn := yoloai.SelectContainerBackend(cmd.Context(), cliutil.ResolveContainerBackendConfig(), cliutil.Layout().CuratedEnv(yoloai.DaemonEnvVars))
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	if !all && len(args) > 0 && !hasWildcard(args[0]) {
		// Only resolve from first arg if it's not a wildcard pattern
		backend = cliutil.ResolveBackendForSandbox(args[0])
	} else if !all && len(args) == 0 {
		if envName := cliutil.SandboxNameFromEnv(); envName != "" {
			backend = cliutil.ResolveBackendForSandbox(envName)
		}
	}

	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		names, err := resolveDestroyNames(cmd, ctx, c, args, all)
		if err != nil {
			return err
		}
		if names == nil {
			// Already handled (empty list with output)
			return nil
		}

		// A sandbox with unapplied changes is only destroyed when
		// --abandon-unapplied authorizes discarding that work. We never
		// prompt to widen the scope, so there is no --yes to paper over it.
		if err := checkActiveWork(cmd, ctx, c, names, abandonUnapplied); err != nil {
			return err
		}

		return executeDestroy(cmd, ctx, c, names, abandonUnapplied)
	})
}

// resolveDestroyNames resolves sandbox names from args or --all, returning nil if already handled.
func resolveDestroyNames(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, args []string, all bool) ([]string, error) {
	if all {
		return resolveDestroyAll(cmd, ctx, c)
	}
	if len(args) == 0 {
		return resolveDestroyFromEnv()
	}
	return resolveDestroyArgs(ctx, c, args)
}

// resolveDestroyAll resolves names when --all is set, returning nil if none exist.
func resolveDestroyAll(cmd *cobra.Command, ctx context.Context, c *yoloai.Client) ([]string, error) {
	infos, err := c.ListSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		if cliutil.JSONEnabled(cmd) {
			return nil, cliutil.WriteJSONList(cmd.OutOrStdout(), "destroyed", []struct{}{})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes to destroy")
		return nil, err
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Environment.Name)
	}
	return names, nil
}

// resolveDestroyFromEnv resolves the sandbox name from the environment when no args are given.
func resolveDestroyFromEnv() ([]string, error) {
	envName := cliutil.SandboxNameFromEnv()
	if envName == "" {
		return nil, yoerrors.NewUsageError("at least one sandbox name is required (or use --all or set YOLOAI_SANDBOX)")
	}
	if err := cliutil.ValidateName(envName); err != nil {
		return nil, err
	}
	return []string{envName}, nil
}

// resolveDestroyArgs expands wildcards and validates each named sandbox arg.
func resolveDestroyArgs(ctx context.Context, c *yoloai.Client, args []string) ([]string, error) {
	var names []string
	for _, arg := range args {
		if hasWildcard(arg) {
			expanded, err := expandWildcard(ctx, c, arg)
			if err != nil {
				return nil, err
			}
			names = append(names, expanded...)
			continue
		}
		if err := cliutil.ValidateName(arg); err != nil {
			return nil, err
		}
		if _, err := c.Sandbox(arg); err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
		names = append(names, arg)
	}
	return names, nil
}

// checkActiveWork refuses the destroy (without prompting) when any named sandbox
// holds unapplied changes, unless --abandon-unapplied authorizes discarding it.
// A running agent alone does not block — only work that would actually be lost.
// Widening the destructive scope is opt-in via the flag alone — there is no
// interactive prompt to answer, so nothing can paper over the safety choice.
func checkActiveWork(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, names []string, abandonUnapplied bool) error {
	if abandonUnapplied {
		return nil
	}
	var warnings []string
	for _, name := range names {
		sb, err := c.Sandbox(name)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("  %s: %v", name, err))
			continue
		}
		active, reason := sb.HasActiveWork(ctx)
		if active {
			warnings = append(warnings, fmt.Sprintf("  %s: %s", name, reason))
		}
	}
	if len(warnings) == 0 {
		return nil
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "The following sandboxes have unapplied changes:") //nolint:errcheck // best-effort output
	for _, w := range warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), w) //nolint:errcheck // best-effort output
	}
	return yoerrors.NewActiveWorkError(
		"%d sandbox(es) have unapplied changes; re-run with --abandon-unapplied or run 'yoloai apply' first",
		len(warnings),
	)
}

// executeDestroy destroys sandboxes and returns an error if any fail.
// abandonUnapplied is threaded into each Destroy call; checkActiveWork has
// already refused if work was present and the flag was absent.
func executeDestroy(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, names []string, abandonUnapplied bool) error {
	type destroyResult struct {
		Name   string `json:"name"`
		Action string `json:"action,omitempty"`
		Error  string `json:"error,omitempty"`
	}

	if cliutil.JSONEnabled(cmd) {
		var results []destroyResult
		for _, name := range names {
			slog.Info("destroying sandbox", "event", "sandbox.destroy", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
			if err := destroyOne(cmd, ctx, c, name, abandonUnapplied); err != nil {
				results = append(results, destroyResult{Name: name, Error: err.Error()})
			} else {
				slog.Info("sandbox destroyed", "event", "sandbox.destroy.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
				results = append(results, destroyResult{Name: name, Action: "destroyed"})
			}
		}
		return cliutil.WriteJSONList(cmd.OutOrStdout(), "destroyed", results)
	}

	var errs []error
	for _, name := range names {
		slog.Info("destroying sandbox", "event", "sandbox.destroy", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
		if err := destroyOne(cmd, ctx, c, name, abandonUnapplied); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: destroy %s: %v\n", name, err) //nolint:errcheck // best-effort output
			errs = append(errs, err)
		} else {
			slog.Info("sandbox destroyed", "event", "sandbox.destroy.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
			fmt.Fprintf(cmd.OutOrStdout(), "Destroyed %s\n", name)                               //nolint:errcheck // best-effort output
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to destroy %d sandbox(es)", len(errs))
	}
	return nil
}

// destroyOne destroys a single sandbox and renders any advisory notices
// (e.g. a directory that couldn't be fully removed). Returns the destroy error,
// if any. abandonUnapplied authorizes discarding unapplied work; when false the
// library refuses a sandbox that still holds it (a backstop to checkActiveWork).
func destroyOne(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, name string, abandonUnapplied bool) error {
	sb, err := c.Sandbox(name)
	if err != nil {
		return err
	}
	res, err := sb.Destroy(ctx, yoloai.SandboxDestroyOptions{AbandonUnappliedWork: abandonUnapplied})
	if res != nil {
		cliutil.RenderNotices(cmd, res.Notices)
	}
	return err
}
