package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exec <name> <command> [args...]",
		Short: "Run a command inside a sandbox",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return sandbox.NewUsageError("command is required")
			}
			cmdArgs := rest

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				info, err := sandbox.InspectSandbox(ctx, rt, name)
				if err != nil {
					return err
				}

				if info.Status != sandbox.StatusRunning {
					return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
				}

				containerName := sandbox.ContainerName(name)
				slog.Debug("exec in container", "container", containerName, "cmd", cmdArgs)

				if err := rt.InteractiveExec(ctx, containerName, cmdArgs, "yoloai"); err != nil {
					var exitErr *exec.ExitError
					if errors.As(err, &exitErr) {
						os.Exit(exitErr.ExitCode())
					}
					return err
				}
				return nil
			})
		},
	}
}
