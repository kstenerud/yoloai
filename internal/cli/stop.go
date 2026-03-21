package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "stop <name>...",
		Short:   "Stop sandboxes (preserving state)",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")

			if all && len(args) > 0 {
				return sandbox.NewUsageError("cannot specify sandbox names with --all")
			}

			// Resolve backend: from first named sandbox, or config default for --all
			backend, warn := detectContainerBackend(resolveContainerBackendConfig())
			if warn != "" {
				fmt.Fprintln(os.Stderr, warn)
			}
			if !all && len(args) > 0 {
				backend = resolveBackendForSandbox(args[0])
			} else if !all {
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
					for _, info := range infos {
						switch info.Status {
						case sandbox.StatusActive, sandbox.StatusIdle, sandbox.StatusDone, sandbox.StatusFailed:
							names = append(names, info.Meta.Name)
						}
					}
					if len(names) == 0 {
						if jsonEnabled(cmd) {
							return writeJSON(cmd.OutOrStdout(), []struct{}{})
						}
						_, err = fmt.Fprintln(cmd.OutOrStdout(), "No running sandboxes to stop")
						return err
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
						for _, name := range args {
							if err := sandbox.ValidateName(name); err != nil {
								return err
							}
						}
						names = args
					}
				}

				if jsonEnabled(cmd) {
					type stopResult struct {
						Name   string `json:"name"`
						Action string `json:"action,omitempty"`
						Error  string `json:"error,omitempty"`
					}
					var results []stopResult
					for _, name := range names {
						slog.Info("stopping sandbox", "event", "sandbox.stop", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
						if err := mgr.Stop(ctx, name); err != nil {
							results = append(results, stopResult{Name: name, Error: err.Error()})
						} else {
							slog.Info("sandbox stopped", "event", "sandbox.stop.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
							results = append(results, stopResult{Name: name, Action: "stopped"})
						}
					}
					return writeJSON(cmd.OutOrStdout(), results)
				}

				var errs []error
				for _, name := range names {
					slog.Info("stopping sandbox", "event", "sandbox.stop", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
					if err := mgr.Stop(ctx, name); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: stop %s: %v\n", name, sandboxErrorHint(name, err)) //nolint:errcheck // best-effort output
						errs = append(errs, err)
					} else {
						slog.Info("sandbox stopped", "event", "sandbox.stop.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
						fmt.Fprintf(cmd.OutOrStdout(), "Stopped %s\n", name)                            //nolint:errcheck // best-effort output
					}
				}

				if len(errs) > 0 {
					return fmt.Errorf("failed to stop %d sandbox(es)", len(errs))
				}
				return nil
			})
		},
	}

	cmd.Flags().Bool("all", false, "Stop all running sandboxes")

	return cmd
}
