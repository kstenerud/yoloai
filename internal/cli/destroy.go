// ABOUTME: Cobra "destroy" command: stops and removes one or more sandboxes,
// ABOUTME: with wildcard expansion, --all, and active-work confirmation logic.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/spf13/cobra"
)

// hasWildcard returns true if the string contains * or ? characters.
func hasWildcard(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// expandWildcard matches a wildcard pattern against all sandbox names.
// Returns matching sandbox names, or an error if no matches found.
func expandWildcard(ctx context.Context, rt runtime.Runtime, pattern string) ([]string, error) {
	infos, err := sandbox.ListSandboxes(ctx, cliLayout(), rt)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", err)
	}

	var matches []string
	for _, info := range infos {
		matched, err := filepath.Match(pattern, info.Meta.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		if matched {
			matches = append(matches, info.Meta.Name)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no sandboxes match pattern %q", pattern)
	}

	return matches, nil
}

func newDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "destroy <name>...",
		Short:   "Stop and remove sandboxes",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE:    runDestroyCmd,
	}

	cmd.Flags().Bool("all", false, "Destroy all sandboxes")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}

func runDestroyCmd(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	yes := effectiveYes(cmd)

	if all && len(args) > 0 {
		return sandbox.NewUsageError("cannot specify sandbox names with --all")
	}

	// Resolve backend: from first named sandbox, or config default for --all/wildcards
	backend, warn := runtime.SelectContainerBackend(cmd.Context(), resolveContainerBackendConfig())
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	if !all && len(args) > 0 && !hasWildcard(args[0]) {
		// Only resolve from first arg if it's not a wildcard pattern
		backend = resolveBackendForSandbox(args[0])
	} else if !all && len(args) == 0 {
		if envName := os.Getenv(EnvSandboxName); envName != "" {
			backend = resolveBackendForSandbox(envName)
		}
	}

	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())

		names, err := resolveDestroyNames(cmd, ctx, rt, args, all)
		if err != nil {
			return err
		}
		if names == nil {
			// Already handled (empty list with output)
			return nil
		}

		// Smart confirmation (unless --yes)
		if !yes {
			if done, confirmErr := confirmDestroy(cmd, ctx, mgr, names); confirmErr != nil {
				return confirmErr
			} else if done {
				return nil
			}
		}

		return executeDestroy(cmd, ctx, mgr, names)
	})
}

// resolveDestroyNames resolves sandbox names from args or --all, returning nil if already handled.
func resolveDestroyNames(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, args []string, all bool) ([]string, error) {
	if all {
		return resolveDestroyAll(cmd, ctx, rt)
	}
	if len(args) == 0 {
		return resolveDestroyFromEnv()
	}
	return resolveDestroyArgs(ctx, rt, args)
}

// resolveDestroyAll resolves names when --all is set, returning nil if none exist.
func resolveDestroyAll(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime) ([]string, error) {
	infos, err := sandbox.ListSandboxes(ctx, cliLayout(), rt)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		if jsonEnabled(cmd) {
			return nil, writeJSON(cmd.OutOrStdout(), []struct{}{})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes to destroy")
		return nil, err
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Meta.Name)
	}
	return names, nil
}

// resolveDestroyFromEnv resolves the sandbox name from the environment when no args are given.
func resolveDestroyFromEnv() ([]string, error) {
	envName := os.Getenv(EnvSandboxName)
	if envName == "" {
		return nil, sandbox.NewUsageError("at least one sandbox name is required (or use --all or set YOLOAI_SANDBOX)")
	}
	if err := store.ValidateName(envName); err != nil {
		return nil, err
	}
	return []string{envName}, nil
}

// resolveDestroyArgs expands wildcards and validates each named sandbox arg.
func resolveDestroyArgs(ctx context.Context, rt runtime.Runtime, args []string) ([]string, error) {
	var names []string
	for _, arg := range args {
		if hasWildcard(arg) {
			expanded, err := expandWildcard(ctx, rt, arg)
			if err != nil {
				return nil, err
			}
			names = append(names, expanded...)
			continue
		}
		if err := store.ValidateName(arg); err != nil {
			return nil, err
		}
		if err := store.RequireSandboxDir(cliLayout().SandboxDir(arg)); err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}
		names = append(names, arg)
	}
	return names, nil
}

// confirmDestroy checks for active work and prompts. Returns true if caller should return nil.
func confirmDestroy(cmd *cobra.Command, ctx context.Context, mgr *sandbox.Manager, names []string) (done bool, err error) {
	var warnings []string
	for _, name := range names {
		needs, reason := mgr.NeedsConfirmation(ctx, name)
		if needs {
			warnings = append(warnings, fmt.Sprintf("  %s: %s", name, reason))
		}
	}
	if len(warnings) == 0 {
		return false, nil
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "The following sandboxes have active work:") //nolint:errcheck // best-effort output
	for _, w := range warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), w) //nolint:errcheck // best-effort output
	}
	// Non-TTY: cannot prompt — return a typed error so CI scripts can detect it.
	if fi, statErr := os.Stdin.Stat(); statErr == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		return false, sandbox.NewActiveWorkError(
			"%d sandbox(es) have active work; use --yes to force or run 'yoloai apply' first",
			len(warnings),
		)
	}
	confirmed, confirmErr := sandbox.Confirm(cmd.Context(), "Destroy all listed sandboxes? [y/N] ", os.Stdin, cmd.ErrOrStderr())
	if confirmErr != nil {
		return false, confirmErr
	}
	return !confirmed, nil
}

// executeDestroy destroys sandboxes and returns an error if any fail.
func executeDestroy(cmd *cobra.Command, ctx context.Context, mgr *sandbox.Manager, names []string) error {
	type destroyResult struct {
		Name   string `json:"name"`
		Action string `json:"action,omitempty"`
		Error  string `json:"error,omitempty"`
	}

	if jsonEnabled(cmd) {
		var results []destroyResult
		for _, name := range names {
			slog.Info("destroying sandbox", "event", "sandbox.destroy", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
			if err := mgr.Destroy(ctx, name); err != nil {
				results = append(results, destroyResult{Name: name, Error: err.Error()})
			} else {
				slog.Info("sandbox destroyed", "event", "sandbox.destroy.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
				results = append(results, destroyResult{Name: name, Action: "destroyed"})
			}
		}
		return writeJSON(cmd.OutOrStdout(), results)
	}

	var errs []error
	for _, name := range names {
		slog.Info("destroying sandbox", "event", "sandbox.destroy", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
		if err := mgr.Destroy(ctx, name); err != nil {
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
