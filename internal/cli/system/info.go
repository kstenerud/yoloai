package system

// ABOUTME: `yoloai system info` subcommand. Displays version, paths,
// ABOUTME: disk usage, and backend availability.

import (
	"fmt"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func newSystemInfoCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show system information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := cliutil.System().Info(cmd.Context())
			if err != nil {
				return err
			}
			if cliutil.JSONEnabled(cmd) {
				return writeSystemInfoJSON(cmd, version, commit, date, info)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Version:     %s\n", version)             //nolint:errcheck
			fmt.Fprintf(out, "Commit:      %s\n", commit)              //nolint:errcheck
			fmt.Fprintf(out, "Built:       %s\n", date)                //nolint:errcheck
			fmt.Fprintf(out, "Config:      %s\n", info.GlobalConfig)   //nolint:errcheck
			fmt.Fprintf(out, "Profile:     %s\n", info.DefaultsConfig) //nolint:errcheck
			fmt.Fprintf(out, "Data dir:    %s\n", info.DataDir)        //nolint:errcheck
			fmt.Fprintf(out, "Sandboxes:   %s\n", info.SandboxesDir)   //nolint:errcheck

			if size, sizeErr := cliutil.DirSize(info.DataDir); sizeErr != nil {
				fmt.Fprintf(out, "Disk usage:  (unavailable)\n") //nolint:errcheck
			} else {
				fmt.Fprintf(out, "Disk usage:  %s\n", cliutil.FormatSize(size)) //nolint:errcheck
			}

			fmt.Fprintln(out)              //nolint:errcheck
			fmt.Fprintln(out, "Backends:") //nolint:errcheck
			for _, b := range info.Backends {
				status := "available"
				if !b.Available {
					status = "unavailable"
					if b.Note != "" {
						status += " (" + b.Note + ")"
					}
				}
				fmt.Fprintf(out, "  %-12s %s\n", b.Name, status) //nolint:errcheck
			}
			return nil
		},
	}
}

// writeSystemInfoJSON outputs system info as JSON.
func writeSystemInfoJSON(cmd *cobra.Command, version, commit, date string, info *yoloai.SystemInfo) error {
	diskUsage := ""
	if size, err := cliutil.DirSize(info.DataDir); err == nil {
		diskUsage = cliutil.FormatSize(size)
	}

	type backendStatus struct {
		Name      string `json:"name"`
		Available bool   `json:"available"`
		Note      string `json:"note,omitempty"`
	}

	backends := make([]backendStatus, 0, len(info.Backends))
	for _, b := range info.Backends {
		backends = append(backends, backendStatus{
			Name:      string(b.Name),
			Available: b.Available,
			Note:      b.Note,
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
		ConfigPath:        info.GlobalConfig,
		ProfileConfigPath: info.DefaultsConfig,
		DataDir:           info.DataDir,
		SandboxesDir:      info.SandboxesDir,
		DiskUsage:         diskUsage,
		Backends:          backends,
	}

	return cliutil.WriteJSON(cmd.OutOrStdout(), result)
}
