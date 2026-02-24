package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <name>",
		Short: "Attach to a sandbox's tmux session",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

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

			switch info.Status {
			case sandbox.StatusRunning, sandbox.StatusDone, sandbox.StatusFailed:
				// OK — user can attach to see output
			default:
				return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
			}

			containerName := "yoloai-" + name
			slog.Debug("attaching to tmux session", "container", containerName)

			c := exec.Command("docker", "exec", "-it", containerName, "tmux", "attach", "-t", "main") //nolint:gosec // G204: containerName is validated sandbox name
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}
