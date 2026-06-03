// ABOUTME: Cobra "exec" command: runs an arbitrary command interactively inside
// ABOUTME: a running sandbox container, propagating the exit code to the host.
package sandboxcmd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"

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

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		slog.Debug("exec in container", "event", "sandbox.exec", "sandbox", name, "cmd", cmdArgs) //nolint:gosec // G706: values are internal, not user-controlled log injection

		sb, err := c.Sandbox(name)
		if err != nil {
			return cliutil.SandboxErrorHint(name, err)
		}
		if err := cliutil.WithTerminal(func(io yoloai.IOStreams) error {
			return sb.Exec(ctx, yoloai.SandboxExecOptions{Command: cmdArgs, PTY: true}, io)
		}); err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
				os.Exit(exitErr.ExitCode())
			}
			return cliutil.SandboxErrorHint(name, err)
		}
		return nil
	})
}
