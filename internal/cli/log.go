package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log <name>",
		Short: "Show sandbox session log",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			sandboxDir := sandbox.Dir(name)
			if _, err := os.Stat(sandboxDir); err != nil {
				return sandbox.ErrSandboxNotFound
			}

			logPath := filepath.Join(sandboxDir, "log.txt")
			f, err := os.Open(logPath) //nolint:gosec // G304: path is constructed from sandbox dir
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "No log output yet") //nolint:errcheck // best-effort output
					return nil
				}
				return fmt.Errorf("open log file: %w", err)
			}
			defer f.Close() //nolint:errcheck // best-effort cleanup

			return RunPager(f)
		},
	}
}
