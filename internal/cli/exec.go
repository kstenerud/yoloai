package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "exec <name> <command> [args...]",
		Short:   "Run a command inside a sandbox",
		GroupID: groupInspect,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return sandbox.NewUsageError("command is required")
			}
			cmdArgs := rest

			ctx := cmd.Context()
			client, err := docker.NewClient(ctx)
			if err != nil {
				return err
			}
			defer client.Close() //nolint:errcheck // best-effort cleanup

			info, err := sandbox.InspectSandbox(ctx, client, name)
			if err != nil {
				return err
			}

			if info.Status != sandbox.StatusRunning {
				return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
			}

			containerName := "yoloai-" + name
			isTTY := term.IsTerminal(int(os.Stdin.Fd())) //nolint:gosec // fd conversion is safe on all supported platforms

			var dockerArgs []string
			dockerArgs = append(dockerArgs, "exec")
			if isTTY {
				dockerArgs = append(dockerArgs, "-it")
			} else {
				dockerArgs = append(dockerArgs, "-i")
			}
			dockerArgs = append(dockerArgs, "-u", "yoloai", containerName)
			dockerArgs = append(dockerArgs, cmdArgs...)

			slog.Debug("exec in container", "container", containerName, "cmd", cmdArgs)

			c := exec.Command("docker", dockerArgs...) //nolint:gosec // G204: dockerArgs built from validated sandbox name and user command
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr

			if err := c.Run(); err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					os.Exit(exitErr.ExitCode())
				}
				return err
			}
			return nil
		},
	}
}
