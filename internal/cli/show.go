package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show sandbox configuration and state",
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

			w := cmd.OutOrStdout()
			meta := info.Meta

			fmt.Fprintf(w, "Name:        %s\n", meta.Name)                              //nolint:errcheck // best-effort output
			fmt.Fprintf(w, "Status:      %s\n", info.Status)                             //nolint:errcheck // best-effort output
			fmt.Fprintf(w, "Agent:       %s\n", meta.Agent)                              //nolint:errcheck // best-effort output

			if meta.Model != "" {
				fmt.Fprintf(w, "Model:       %s\n", meta.Model)                          //nolint:errcheck // best-effort output
			}

			sandboxDir := sandbox.Dir(name)
			if preview := loadPromptPreview(sandboxDir); preview != "" {
				fmt.Fprintf(w, "Prompt:      %s\n", preview)                             //nolint:errcheck // best-effort output
			}

			fmt.Fprintf(w, "Workdir:     %s (%s)\n", meta.Workdir.HostPath, meta.Workdir.Mode) //nolint:errcheck // best-effort output

			if meta.NetworkMode != "" {
				fmt.Fprintf(w, "Network:     %s\n", meta.NetworkMode)                    //nolint:errcheck // best-effort output
			}
			if len(meta.Ports) > 0 {
				fmt.Fprintf(w, "Ports:       %s\n", strings.Join(meta.Ports, ", "))      //nolint:errcheck // best-effort output
			}

			fmt.Fprintf(w, "Created:     %s (%s)\n", meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), sandbox.FormatAge(meta.CreatedAt)) //nolint:errcheck // best-effort output

			if meta.Workdir.BaselineSHA != "" {
				fmt.Fprintf(w, "Baseline:    %s\n", meta.Workdir.BaselineSHA)            //nolint:errcheck // best-effort output
			}
			if info.ContainerID != "" {
				fmt.Fprintf(w, "Container:   %s\n", info.ContainerID)                    //nolint:errcheck // best-effort output
			}

			fmt.Fprintf(w, "Changes:     %s\n", info.HasChanges)                        //nolint:errcheck // best-effort output

			slog.Debug("show complete", "sandbox", name)
			return nil
		},
	}
}

// loadPromptPreview reads prompt.txt and returns the first 200 characters.
func loadPromptPreview(sandboxDir string) string {
	data, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // G304: path is constructed from sandbox dir
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
