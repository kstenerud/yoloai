// ABOUTME: Generates sandbox context file and per-agent reference files.
// ABOUTME: Context describes the sandbox environment (dirs, network, resources)
// ABOUTME: so AI agents understand their constraints without trial and error.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
)

// GenerateContext builds a markdown description of the sandbox environment
// from Meta fields. Sections are omitted when they have no content.
func GenerateContext(meta *Meta) string {
	var b strings.Builder

	b.WriteString("# Sandbox Environment\n\n")
	b.WriteString("You are in a yoloAI sandbox. Changes in copy-mode directories are isolated — the user reviews them with `yoloai diff` before applying to the host.\n")

	// Directories section (always present — at minimum there's a workdir)
	b.WriteString("\n## Directories\n\n")
	writeDir(&b, meta.Workdir.MountPath, meta.Workdir.HostPath, meta.Workdir.Mode, true)
	for _, d := range meta.Directories {
		writeDir(&b, d.MountPath, d.HostPath, d.Mode, false)
	}

	// Network section (only when network mode is set)
	if meta.NetworkMode != "" {
		b.WriteString("\n## Network\n\n")
		switch meta.NetworkMode {
		case "none":
			b.WriteString("No network access.\n")
		case "isolated":
			if len(meta.NetworkAllow) > 0 {
				b.WriteString("Isolated. Allowed domains: ")
				b.WriteString(strings.Join(meta.NetworkAllow, ", "))
				b.WriteString("\n")
			} else {
				b.WriteString("Isolated. No domains allowed.\n")
			}
		}
	}

	// Resources section (only when resources are set)
	if meta.Resources != nil {
		var parts []string
		if meta.Resources.CPUs != "" {
			parts = append(parts, meta.Resources.CPUs+" cpus")
		}
		if meta.Resources.Memory != "" {
			parts = append(parts, meta.Resources.Memory+" memory")
		}
		if len(parts) > 0 {
			b.WriteString("\n## Resources\n\n")
			b.WriteString(strings.Join(parts, ", "))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// writeDir writes a single directory line to the builder.
func writeDir(b *strings.Builder, mountPath, hostPath, mode string, isWorkdir bool) {
	b.WriteString("- ")
	b.WriteString(mountPath)

	if mountPath != hostPath {
		fmt.Fprintf(b, " → %s", hostPath)
	}

	b.WriteString(" (")
	b.WriteString(mode)
	b.WriteString(")")

	if isWorkdir {
		b.WriteString(" ← working directory")
	}

	b.WriteString("\n")
}

// WriteContextFiles writes the sandbox context file and optional per-agent
// instruction file into the sandbox directory.
func WriteContextFiles(sandboxDir string, meta *Meta, agentDef *agent.Definition) error {
	content := GenerateContext(meta)

	// Write context.md at sandbox root (reference copy)
	contextPath := filepath.Join(sandboxDir, "context.md")
	if err := os.WriteFile(contextPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("write context.md: %w", err)
	}

	// Write full context inline into the agent's native instruction file
	if agentDef.ContextFile != "" && agentDef.StateDir != "" {
		refPath := filepath.Join(sandboxDir, "agent-state", agentDef.ContextFile)
		if err := os.WriteFile(refPath, []byte(content), 0600); err != nil {
			return fmt.Errorf("write agent context file %s: %w", agentDef.ContextFile, err)
		}
	}

	return nil
}
