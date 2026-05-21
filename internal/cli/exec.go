// ABOUTME: Cobra "exec" command: runs an arbitrary command interactively inside
// ABOUTME: a running sandbox container, propagating the exit code to the host.
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
	"github.com/kstenerud/yoloai/sandbox/store"
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
	defer openCLIJSONLSink(name, cmd)()
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

		containerName := store.InstanceName(name)
		slog.Debug("exec in container", "event", "sandbox.exec", "container", containerName, "cmd", cmdArgs) //nolint:gosec // G706: values are internal, not user-controlled log injection

		if err := rt.InteractiveExec(ctx, containerName, cmdArgs, tmuxExecUser(info.Meta), info.Meta.Workdir.MountPath); err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
				os.Exit(exitErr.ExitCode())
			}
			return err
		}
		return nil
	})
}
