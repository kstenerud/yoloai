package sandboxcmd

// ABOUTME: `yoloai sandbox <name> info` handler. Shows detailed sandbox
// ABOUTME: configuration and state.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

func runSandboxInfo(cmd *cobra.Command, name string) error {
	closeSink := cliutil.OpenCLIJSONLSink(name, cmd)
	defer closeSink()
	slog.Info("collecting sandbox info", "event", "sandbox.info", "sandbox", name) //nolint:gosec // G706: name is an internal sandbox name, not user-injected log data
	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		info, err := sb.Inspect(ctx)
		if err != nil {
			return cliutil.SandboxErrorHint(name, err)
		}

		if cliutil.JSONEnabled(cmd) {
			type infoJSON struct {
				*yoloai.SandboxInfo
				ConfigPath    string `json:"config_path"`
				PromptPreview string `json:"prompt_preview,omitempty"`
			}
			result := infoJSON{
				SandboxInfo:   info,
				ConfigPath:    sb.RuntimeConfigPath(),
				PromptPreview: loadPromptPreview(sb),
			}
			return cliutil.WriteJSON(cmd.OutOrStdout(), result)
		}

		printSandboxInfo(cmd, sb, name, info)
		slog.Debug("show complete", "event", "sandbox.info", "sandbox", name) //nolint:gosec // G706: name is an internal sandbox name, not user-injected log data
		return nil
	})
}

// printSandboxInfo prints sandbox info in human-readable format.
func printSandboxInfo(cmd *cobra.Command, sb *yoloai.Sandbox, name string, info *yoloai.SandboxInfo) {
	w := cmd.OutOrStdout()
	meta := info.Environment

	fmt.Fprintf(w, "Name:        %s\n", meta.Name)      //nolint:errcheck
	fmt.Fprintf(w, "Status:      %s\n", info.Status)    //nolint:errcheck
	fmt.Fprintf(w, "Agent:       %s\n", meta.AgentType) //nolint:errcheck

	if meta.Model != "" {
		fmt.Fprintf(w, "Model:       %s\n", meta.Model) //nolint:errcheck
	}
	fmt.Fprintf(w, "Backend:     %s\n", meta.BackendType) //nolint:errcheck
	if meta.Isolation != "" {
		fmt.Fprintf(w, "Isolation:   %s\n", meta.Isolation) //nolint:errcheck
	}
	if meta.Profile != "" {
		fmt.Fprintf(w, "Profile:     %s\n", meta.Profile) //nolint:errcheck
	}

	sandboxDir := cliutil.Layout().SandboxDir(name)
	fmt.Fprintf(w, "Sandbox dir: %s\n", sandboxDir)             //nolint:errcheck
	fmt.Fprintf(w, "Config:      %s\n", sb.RuntimeConfigPath()) //nolint:errcheck

	if preview := loadPromptPreview(sb); preview != "" {
		fmt.Fprintf(w, "Prompt:      %s\n", preview) //nolint:errcheck
	}

	printSandboxDirs(w, meta)
	printSandboxNetwork(w, meta)
	printSandboxResources(w, meta, info)
}

// printSandboxDirs prints workdir and auxiliary directory information.
func printSandboxDirs(w io.Writer, meta *yoloai.Environment) {
	if meta.Workdir.MountPath != "" && meta.Workdir.MountPath != meta.Workdir.HostPath {
		fmt.Fprintf(w, "Workdir:     %s → %s (%s)\n", meta.Workdir.HostPath, meta.Workdir.MountPath, meta.Workdir.Mode) //nolint:errcheck
	} else {
		fmt.Fprintf(w, "Workdir:     %s (%s)\n", meta.Workdir.HostPath, meta.Workdir.Mode) //nolint:errcheck
	}
	for _, d := range meta.Directories {
		if d.MountPath != d.HostPath {
			fmt.Fprintf(w, "Dir:         %s → %s (%s)\n", d.HostPath, d.MountPath, d.Mode) //nolint:errcheck
		} else {
			fmt.Fprintf(w, "Dir:         %s (%s)\n", d.HostPath, d.Mode) //nolint:errcheck
		}
	}
}

// printSandboxNetwork prints network mode and port information.
func printSandboxNetwork(w io.Writer, meta *yoloai.Environment) {
	if meta.NetworkMode != "" {
		if meta.NetworkMode == "isolated" && len(meta.NetworkAllow) > 0 {
			fmt.Fprintf(w, "Network:     isolated (%s)\n", strings.Join(meta.NetworkAllow, ", ")) //nolint:errcheck
		} else {
			fmt.Fprintf(w, "Network:     %s\n", meta.NetworkMode) //nolint:errcheck
		}
	}
	if len(meta.Ports) > 0 {
		fmt.Fprintf(w, "Ports:       %s\n", strings.Join(meta.Ports, ", ")) //nolint:errcheck
	}
}

// printSandboxResources prints resource limits and summary information.
func printSandboxResources(w io.Writer, meta *yoloai.Environment, info *yoloai.SandboxInfo) {
	if meta.Resources != nil {
		var parts []string
		if meta.Resources.CPULimit != "" {
			parts = append(parts, meta.Resources.CPULimit+" cpus")
		}
		if meta.Resources.MemoryLimit != "" {
			parts = append(parts, meta.Resources.MemoryLimit+" memory")
		}
		if len(parts) > 0 {
			fmt.Fprintf(w, "Resources:   %s\n", strings.Join(parts, ", ")) //nolint:errcheck
		}
	}
	fmt.Fprintf(w, "Created:     %s (%s)\n", meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), cliutil.FormatAge(meta.CreatedAt)) //nolint:errcheck
	if meta.Workdir.BaselineSHA != "" {
		fmt.Fprintf(w, "Baseline:    %s\n", meta.Workdir.BaselineSHA) //nolint:errcheck
	}
	fmt.Fprintf(w, "Disk Usage:  %s\n", cliutil.FormatDiskUsage(info.DiskUsageBytes)) //nolint:errcheck
	fmt.Fprintf(w, "Changes:     %s\n", info.Changes)                                 //nolint:errcheck
}

// loadPromptPreview reads the stored prompt via the public verb and returns a
// single-line preview for display ("" when no prompt is set).
func loadPromptPreview(sb *yoloai.Sandbox) string {
	prompt, ok, err := sb.Agent().Prompt()
	if err != nil || !ok {
		return ""
	}
	return formatPromptPreview(prompt)
}

// formatPromptPreview collapses newlines to spaces and truncates to 200 runes.
func formatPromptPreview(raw string) string {
	content := strings.ReplaceAll(raw, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")

	runes := []rune(content)
	if len(runes) > 200 {
		return string(runes[:200]) + "..."
	}
	return content
}
