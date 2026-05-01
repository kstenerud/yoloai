// ABOUTME: Generates sandbox context file and per-agent reference files.
// ABOUTME: Context describes the sandbox environment (dirs, network, resources)
// ABOUTME: so AI agents understand their constraints without trial and error.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"

	"github.com/kstenerud/yoloai/agent"
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

	// Files section (exchange directory is always available)
	b.WriteString("\n## Files\n\n")
	filesPath := "/yoloai/files/"
	cachePath := "/yoloai/cache/"
	if meta.HostFilesystem {
		filesPath = filepath.Join(Dir(meta.Name), "files") + "/"
		cachePath = filepath.Join(Dir(meta.Name), "cache") + "/"
	}
	fmt.Fprintf(&b, "The **shared files directory** is at `%s`.\n", filesPath)
	fmt.Fprintf(&b, "Files shared via `yoloai files put` appear here, and anything you write here can be retrieved by the user with `yoloai files get`.\n")
	fmt.Fprintf(&b, "Use this for artifacts the user needs to see — generated reports, exported files, etc.\n")

	// Cache section
	b.WriteString("\n## Cache\n\n")
	fmt.Fprintf(&b, "The **cache directory** is at `%s`.\n", cachePath)
	b.WriteString("Use this for anything that speeds up your work but the user doesn't need to see:\n\n")
	b.WriteString("- **HTTP responses.** Cache fetched web pages/API responses here so you don't re-fetch the same URL. Check the cache before every fetch.\n")
	b.WriteString("- **Git repos.** When you need to search a remote codebase, `git clone --depth 1` into the cache directory and search locally instead of fetching files over HTTPS.\n")
	b.WriteString("- **Any reusable data.** Downloaded archives, parsed documentation, intermediate results, etc.\n")

	// Terminology
	b.WriteString("\n## Terminology\n\n")
	b.WriteString("When the user says:\n\n")
	fmt.Fprintf(&b, "- \"the cache\" — they mean the cache directory (`%s`)\n", cachePath)
	fmt.Fprintf(&b, "- \"the files dir\" or \"shared files\" — they mean the shared files directory (`%s`)\n", filesPath)

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

	// Debug section (only when --debug is enabled)
	if meta.Debug {
		rtDir := runtimeDir(meta)
		b.WriteString("\n## Idle Detection Debugging\n\n")
		fmt.Fprintf(&b, "This sandbox has `--debug` enabled. The idle detection monitor writes detailed logs to `%s/logs/monitor.jsonl`.\n\n", rtDir)
		b.WriteString("If the user asks you to help debug idle detection (e.g. status stuck on active/idle), check these files:\n\n")
		fmt.Fprintf(&b, "- `%s/logs/monitor.jsonl` — per-cycle trace: each detector's result, stability counters, final decision\n", rtDir)
		fmt.Fprintf(&b, "- `%s/%s` — current status written by the monitor\n", rtDir, AgentStatusFile)
		fmt.Fprintf(&b, "- `%s/%s` — sandbox config including detector stack (`detectors` field) and idle settings\n", rtDir, RuntimeConfigFile)
		fmt.Fprintf(&b, "\nYou can also run `%s/%s/diagnose-idle.sh` for a point-in-time snapshot of all idle detection state.\n", rtDir, BinDir)
	}

	return b.String()
}

// runtimeDir returns the base path where runtime files live for this sandbox.
// Container/VM backends use /yoloai inside the container; host-filesystem backends
// (seatbelt, future SSH) use the sandbox directory on the host.
func runtimeDir(meta *Meta) string {
	if meta.HostFilesystem {
		return Dir(meta.Name)
	}
	return "/yoloai"
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
	if err := fileutil.WriteFile(contextPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("write context.md: %w", err)
	}

	// Embedded agent files (e.g. pi's status hook extension). Paths are
	// relative to AgentRuntimeDir, which is bind-mounted at the agent's
	// StateDir inside the container.
	for relPath, fileContent := range agentDef.EmbeddedFiles {
		targetPath := filepath.Join(sandboxDir, AgentRuntimeDir, relPath)
		if err := fileutil.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
			return fmt.Errorf("create dir for embedded file %s: %w", relPath, err)
		}
		if err := fileutil.WriteFile(targetPath, fileContent, 0600); err != nil {
			return fmt.Errorf("write embedded file %s: %w", relPath, err)
		}
	}

	// Write full context inline into the agent's native instruction file
	if agentDef.ContextFile != "" && agentDef.StateDir != "" {
		refPath := filepath.Join(sandboxDir, AgentRuntimeDir, agentDef.ContextFile)
		if err := fileutil.WriteFile(refPath, []byte(content), 0600); err != nil {
			return fmt.Errorf("write agent context file %s: %w", agentDef.ContextFile, err)
		}

		// Append Q&A protocol section to Claude's CLAUDE.md so it knows
		// how to ask questions through the file exchange directory.
		if agentDef.ContextFile == "CLAUDE.md" {
			qa := `
## yoloAI File Exchange Protocol

You are running inside a yoloAI sandbox. A file exchange directory is
available at ` + "`/yoloai/files/`" + ` — readable and writable from both inside
and outside the sandbox.

**When you need to ask a question or need input to continue:**

1. Write your question to ` + "`/yoloai/files/question.json`" + `:
   ` + "```json" + `
   {"question": "your question here", "context": "optional context"}
   ` + "```" + `
2. Poll ` + "`/yoloai/files/answer.json`" + ` every 5 seconds until it appears.
3. Read the answer and continue your task.

Do not make assumptions about blocking decisions. Write the question file
and wait. The question will be seen and answered by an external agent or user.
`
			f, err := fileutil.OpenFile(refPath, os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // path is from sandbox dir, controlled by yoloai
			if err != nil {
				return fmt.Errorf("open agent context file %s for append: %w", agentDef.ContextFile, err)
			}
			_, writeErr := f.WriteString(qa)
			closeErr := f.Close()
			if writeErr != nil {
				return fmt.Errorf("append Q&A protocol to %s: %w", agentDef.ContextFile, writeErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close agent context file %s: %w", agentDef.ContextFile, closeErr)
			}
		}
	}

	return nil
}
