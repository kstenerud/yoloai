package cli

// ABOUTME: `yoloai sandbox info` subcommand. Shows detailed sandbox
// ABOUTME: configuration and state.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <name>",
		Short: "Show sandbox configuration and state",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				info, err := sandbox.InspectSandbox(ctx, rt, name)
				if err != nil {
					return err
				}

				w := cmd.OutOrStdout()
				meta := info.Meta

				fmt.Fprintf(w, "Name:        %s\n", meta.Name)   //nolint:errcheck
				fmt.Fprintf(w, "Status:      %s\n", info.Status) //nolint:errcheck
				fmt.Fprintf(w, "Agent:       %s\n", meta.Agent)  //nolint:errcheck

				if meta.Model != "" {
					fmt.Fprintf(w, "Model:       %s\n", meta.Model) //nolint:errcheck
				}

				fmt.Fprintf(w, "Backend:     %s\n", meta.Backend) //nolint:errcheck

				sandboxDir := sandbox.Dir(name)
				fmt.Fprintf(w, "Sandbox dir: %s\n", sandboxDir) //nolint:errcheck

				if preview := loadPromptPreview(sandboxDir); preview != "" {
					fmt.Fprintf(w, "Prompt:      %s\n", preview) //nolint:errcheck
				}

				fmt.Fprintf(w, "Workdir:     %s (%s)\n", meta.Workdir.HostPath, meta.Workdir.Mode) //nolint:errcheck

				if meta.NetworkMode != "" {
					fmt.Fprintf(w, "Network:     %s\n", meta.NetworkMode) //nolint:errcheck
				}
				if len(meta.Ports) > 0 {
					fmt.Fprintf(w, "Ports:       %s\n", strings.Join(meta.Ports, ", ")) //nolint:errcheck
				}

				fmt.Fprintf(w, "Created:     %s (%s)\n", meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), sandbox.FormatAge(meta.CreatedAt)) //nolint:errcheck

				if meta.Workdir.BaselineSHA != "" {
					fmt.Fprintf(w, "Baseline:    %s\n", meta.Workdir.BaselineSHA) //nolint:errcheck
				}
				if info.ContainerID != "" {
					fmt.Fprintf(w, "Container:   %s\n", info.ContainerID) //nolint:errcheck
				}

				fmt.Fprintf(w, "Disk Usage:  %s\n", info.DiskUsage)  //nolint:errcheck
				fmt.Fprintf(w, "Changes:     %s\n", info.HasChanges) //nolint:errcheck

				slog.Debug("show complete", "sandbox", name)
				return nil
			})
		},
	}
}

// loadPromptPreview reads prompt.txt and returns the first 200 characters.
func loadPromptPreview(sandboxDir string) string {
	data, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec
	if err != nil {
		return ""
	}

	content := string(data)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")

	runes := []rune(content)
	if len(runes) > 200 {
		return string(runes[:200]) + "..."
	}
	return content
}
