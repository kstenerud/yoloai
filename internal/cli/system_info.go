package cli

// ABOUTME: `yoloai system info` subcommand. Displays version, paths,
// ABOUTME: disk usage, and backend availability.

import (
	"fmt"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSystemInfoCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show system information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonEnabled(cmd) {
				return writeSystemInfoJSON(cmd, version, commit, date)
			}

			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "Version:     %s\n", version) //nolint:errcheck
			fmt.Fprintf(out, "Commit:      %s\n", commit)  //nolint:errcheck
			fmt.Fprintf(out, "Built:       %s\n", date)    //nolint:errcheck

			globalConfigPath := config.GlobalConfigPath()
			fmt.Fprintf(out, "Config:      %s\n", globalConfigPath) //nolint:errcheck

			profileConfigPath := config.ConfigPath()
			fmt.Fprintf(out, "Profile:     %s\n", profileConfigPath) //nolint:errcheck

			dataDir := config.YoloaiDir()
			sandboxesDir := config.SandboxesDir()

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

// writeSystemInfoJSON outputs system info as JSON.
func writeSystemInfoJSON(cmd *cobra.Command, version, commit, date string) error {
	globalConfigPath := config.GlobalConfigPath()
	profileConfigPath := config.ConfigPath()
	dataDir := config.YoloaiDir()
	sandboxesDir := config.SandboxesDir()

	diskUsage := ""
	if size, err := sandbox.DirSize(dataDir); err == nil {
		diskUsage = sandbox.FormatSize(size)
	}

	type backendStatus struct {
		Name      string `json:"name"`
		Available bool   `json:"available"`
		Note      string `json:"note,omitempty"`
	}

	var backends []backendStatus
	ctx := cmd.Context()
	for _, b := range knownBackends {
		available, note := checkBackend(ctx, b.Name)
		backends = append(backends, backendStatus{
			Name:      b.Name,
			Available: available,
			Note:      note,
		})
	}

	result := struct {
		Version           string          `json:"version"`
		Commit            string          `json:"commit"`
		Date              string          `json:"date"`
		ConfigPath        string          `json:"config_path"`
		ProfileConfigPath string          `json:"profile_config_path"`
		DataDir           string          `json:"data_dir"`
		SandboxesDir      string          `json:"sandboxes_dir"`
		DiskUsage         string          `json:"disk_usage"`
		Backends          []backendStatus `json:"backends"`
	}{
		Version:           version,
		Commit:            commit,
		Date:              date,
		ConfigPath:        globalConfigPath,
		ProfileConfigPath: profileConfigPath,
		DataDir:           dataDir,
		SandboxesDir:      sandboxesDir,
		DiskUsage:         diskUsage,
		Backends:          backends,
	}

	return writeJSON(cmd.OutOrStdout(), result)
}
