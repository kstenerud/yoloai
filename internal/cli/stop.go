package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
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

			return withRuntime(cmd, func(ctx context.Context, rt runtime.Runtime) error {
				backend := resolveBackend(cmd)
				mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())

				var names []string
				if all {
					infos, err := sandbox.ListSandboxes(ctx, rt)
					if err != nil {
						return err
					}
					for _, info := range infos {
						switch info.Status {
						case sandbox.StatusRunning, sandbox.StatusDone, sandbox.StatusFailed:
							names = append(names, info.Meta.Name)
						}
					}
					if len(names) == 0 {
						_, err = fmt.Fprintln(cmd.OutOrStdout(), "No running sandboxes to stop")
						return err
					}
				} else {
					if len(args) == 0 {
						envName := os.Getenv(EnvSandboxName)
						if envName == "" {
							return sandbox.NewUsageError("at least one sandbox name is required (or use --all or set YOLOAI_SANDBOX)")
						}
						names = []string{envName}
					} else {
						names = args
					}
				}

				var errs []error
				for _, name := range names {
					if err := mgr.Stop(ctx, name); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: stop %s: %v\n", name, err) //nolint:errcheck // best-effort output
						errs = append(errs, err)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "Stopped %s\n", name) //nolint:errcheck // best-effort output
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
