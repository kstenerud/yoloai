package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func runExec(cmd *cobra.Command, args []string) error {
	if jsonEnabled(cmd) {
		return errJSONNotSupported("exec")
	}

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
			return sandboxErrorHint(name, err)
		}

		if info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
			return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
		}

		containerName := sandbox.InstanceName(name)
		slog.Debug("exec in container", "container", containerName, "cmd", cmdArgs) //nolint:gosec // G706: values are internal, not user-controlled log injection

		// Use container's default user (not yoloai) for Podman --userns=keep-id compatibility
		if err := rt.InteractiveExec(ctx, containerName, cmdArgs, "", info.Meta.Workdir.MountPath); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				os.Exit(exitErr.ExitCode())
			}
			return err
		}
		return nil
	})
}
