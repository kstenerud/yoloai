// ABOUTME: Cobra "exec" command: runs an arbitrary command interactively inside
// ABOUTME: a running sandbox container, propagating the exit code to the host.
package sandboxcmd

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

func runExec(cmd *cobra.Command, args []string) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.ErrJSONNotSupported("exec")
	}

	name, rest, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()
	if len(rest) == 0 {
		return yoerrors.NewUsageError("command is required")
	}
	cmdArgs := rest

	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		slog.Debug("exec in container", "event", "sandbox.exec", "sandbox", name, "cmd", cmdArgs)

		if err := cliutil.WithTerminal(func(io yoloai.IOStreams) error {
			return sb.Exec(ctx, yoloai.SandboxExecOptions{Command: cmdArgs, PTY: true}, io)
		}); err != nil {
			if exitErr, ok := errors.AsType[*yoloai.ExecExitError](err); ok {
				os.Exit(exitErr.ExitCode())
			}
			return cliutil.SandboxErrorHint(name, err)
		}
		return nil
	})
}
