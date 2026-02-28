package cli

// ABOUTME: `yoloai system info` subcommand. Displays version, paths,
// ABOUTME: disk usage, and backend availability.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSystemInfoCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show system information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "Version:     %s\n", version) //nolint:errcheck
			fmt.Fprintf(out, "Commit:      %s\n", commit)  //nolint:errcheck
			fmt.Fprintf(out, "Built:       %s\n", date)    //nolint:errcheck

			configPath, err := sandbox.ConfigPath()
			if err != nil {
				configPath = "(unknown)"
			}
			fmt.Fprintf(out, "Config:      %s\n", configPath) //nolint:errcheck

			homeDir, err := os.UserHomeDir()
			if err != nil {
				homeDir = "(unknown)"
			}
			dataDir := filepath.Join(homeDir, ".yoloai")
			sandboxesDir := filepath.Join(dataDir, "sandboxes")

			fmt.Fprintf(out, "Data dir:    %s\n", dataDir)      //nolint:errcheck
			fmt.Fprintf(out, "Sandboxes:   %s\n", sandboxesDir) //nolint:errcheck

			size, err := sandbox.DirSize(dataDir)
			if err != nil {
				fmt.Fprintf(out, "Disk usage:  (unavailable)\n") //nolint:errcheck
			} else {
				fmt.Fprintf(out, "Disk usage:  %s\n", sandbox.FormatSize(size)) //nolint:errcheck
			}

			fmt.Fprintln(out)              //nolint:errcheck
			fmt.Fprintln(out, "Backends:") //nolint:errcheck
			ctx := cmd.Context()
			for _, b := range knownBackends {
				available, note := checkBackend(ctx, b.Name)
				status := "available"
				if !available {
					status = "unavailable"
					if note != "" {
						status += " (" + note + ")"
					}
				}
				fmt.Fprintf(out, "  %-12s %s\n", b.Name, status) //nolint:errcheck
			}

			return nil
		},
	}
}
