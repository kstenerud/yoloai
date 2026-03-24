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
	"github.com/spf13/cobra"
)

// hasWildcard returns true if the string contains * or ? characters.
func hasWildcard(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// expandWildcard matches a wildcard pattern against all sandbox names.
// Returns matching sandbox names, or an error if no matches found.
func expandWildcard(ctx context.Context, rt runtime.Runtime, pattern string) ([]string, error) {
	infos, err := sandbox.ListSandboxes(ctx, rt)
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
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			yes := effectiveYes(cmd)

			if all && len(args) > 0 {
				return sandbox.NewUsageError("cannot specify sandbox names with --all")
			}

			// Resolve backend: from first named sandbox, or config default for --all/wildcards
			backend, warn := detectContainerBackend(resolveContainerBackendConfig())
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

				var names []string
				if all {
					infos, err := sandbox.ListSandboxes(ctx, rt)
					if err != nil {
						return err
					}
					if len(infos) == 0 {
						if jsonEnabled(cmd) {
							return writeJSON(cmd.OutOrStdout(), []struct{}{})
						}
						_, err = fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes to destroy")
						return err
					}
					for _, info := range infos {
						names = append(names, info.Meta.Name)
					}
				} else {
					if len(args) == 0 {
						envName := os.Getenv(EnvSandboxName)
						if envName == "" {
							return sandbox.NewUsageError("at least one sandbox name is required (or use --all or set YOLOAI_SANDBOX)")
						}
						if err := sandbox.ValidateName(envName); err != nil {
							return err
						}
						names = []string{envName}
					} else {
						// Expand wildcards in args
						for _, arg := range args {
							if hasWildcard(arg) {
								expanded, err := expandWildcard(ctx, rt, arg)
								if err != nil {
									return err
								}
								names = append(names, expanded...)
							} else {
								if err := sandbox.ValidateName(arg); err != nil {
									return err
								}
								if _, err := sandbox.RequireSandboxDir(arg); err != nil {
									return fmt.Errorf("%s: %w", arg, err)
								}
								names = append(names, arg)
							}
						}
					}
				}

				// Smart confirmation (unless --yes)
				if !yes {
					var warnings []string
					for _, name := range names {
						needs, reason := mgr.NeedsConfirmation(ctx, name)
						if needs {
							warnings = append(warnings, fmt.Sprintf("  %s: %s", name, reason))
						}
					}
					if len(warnings) > 0 {
						fmt.Fprintln(cmd.ErrOrStderr(), "The following sandboxes have active work:") //nolint:errcheck // best-effort output
						for _, w := range warnings {
							fmt.Fprintln(cmd.ErrOrStderr(), w) //nolint:errcheck // best-effort output
						}
						// Non-TTY: cannot prompt — return a typed error so CI scripts can detect it.
						if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
							return sandbox.NewActiveWorkError(
								"%d sandbox(es) have active work; use --yes to force or run 'yoloai apply' first",
								len(warnings),
							)
						}
						confirmed, confirmErr := sandbox.Confirm(cmd.Context(), "Destroy all listed sandboxes? [y/N] ", os.Stdin, cmd.ErrOrStderr())
						if confirmErr != nil {
							return confirmErr
						}
						if !confirmed {
							return nil
						}
					}
				}

				if jsonEnabled(cmd) {
					type destroyResult struct {
						Name   string `json:"name"`
						Action string `json:"action,omitempty"`
						Error  string `json:"error,omitempty"`
					}
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
			})
		},
	}

	cmd.Flags().Bool("all", false, "Destroy all sandboxes")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}
