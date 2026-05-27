// ABOUTME: Cobra "exec" command: runs an arbitrary command interactively inside
// ABOUTME: a running sandbox container, propagating the exit code to the host.
package cli

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
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
	return withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		slog.Debug("exec in container", "event", "sandbox.exec", "sandbox", name, "cmd", cmdArgs) //nolint:gosec // G706: values are internal, not user-controlled log injection

		if err := c.Exec(ctx, name, cmdArgs, cliIOStreams()); err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
				os.Exit(exitErr.ExitCode())
			}
			return sandboxErrorHint(name, err)
		}
		return nil
	})
}
