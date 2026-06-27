// ABOUTME: Assembles and delivers the sandbox context (the DEF) — a markdown
// ABOUTME: description of the environment + the file-exchange protocol — into the
// ABOUTME: agent's native context file. Agent-agnostic: keyed on EnvSpec.ContextFile.
package envsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// GenerateContext builds a markdown description of the sandbox environment
// from Environment fields. Sections are omitted when they have no content.
// sandboxDir is the per-sandbox state directory (used to compute
// host-filesystem file/cache paths for backends that don't have a
// runtime mount).
func GenerateContext(sandboxDir string, meta *store.Environment) string {
	var b strings.Builder

	b.WriteString("# Sandbox Environment\n\n")
	b.WriteString("You are in a yoloAI sandbox. Changes in copy-mode directories are isolated — the user reviews them with `yoloai diff` before applying to the host.\n")

	// Directories section (always present — at minimum there's a workdir)
	b.WriteString("\n## Directories\n\n")
	writeDir(&b, meta.Workdir().MountPath, meta.Workdir().HostPath, meta.Workdir().Mode, true)
	for _, d := range meta.AuxDirs() {
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
	rtDir := runtimeDir(sandboxDir, meta)
	filesPath := rtDir + "/files/"
	cachePath := rtDir + "/cache/"
	if meta.HostFilesystem {
		filesPath = filepath.Join(sandboxDir, "files") + "/"
		cachePath = filepath.Join(sandboxDir, "cache") + "/"
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

	// Docker-in-Docker section (privileged mode only)
	if meta.Isolation == "container-privileged" {
		b.WriteString("\n## Nested Containers (Docker-in-Docker)\n\n")
		b.WriteString("This sandbox runs in privileged mode. Docker CE and the Compose plugin are pre-installed.\n")
		b.WriteString("`/var/lib/docker` is backed by a real filesystem, so the daemon auto-selects the\n")
		b.WriteString("native overlay storage driver — no configuration needed.\n\n")
		b.WriteString("```bash\n")
		b.WriteString("sudo dockerd &   # auto-selects the overlay2 storage driver\n")
		b.WriteString("docker run <image>\n")
		b.WriteString("docker compose up\n")
		b.WriteString("```\n")
	}

	// Debug section (only when --debug is enabled)
	if meta.Debug {
		b.WriteString("\n## Idle Detection Debugging\n\n")
		fmt.Fprintf(&b, "This sandbox has `--debug` enabled. The idle detection monitor writes detailed logs to `%s/logs/monitor.jsonl`.\n\n", rtDir)
		b.WriteString("If the user asks you to help debug idle detection (e.g. status stuck on active/idle), check these files:\n\n")
		fmt.Fprintf(&b, "- `%s/logs/monitor.jsonl` — per-cycle trace: each detector's result, stability counters, final decision\n", rtDir)
		fmt.Fprintf(&b, "- `%s/%s` — current status written by the monitor\n", rtDir, store.AgentStatusFile)
		fmt.Fprintf(&b, "- `%s/%s` — sandbox config including detector stack (`detectors` field) and idle settings\n", rtDir, store.RuntimeConfigFile)
		fmt.Fprintf(&b, "\nYou can also run `%s/%s/diagnose-idle.sh` for a point-in-time snapshot of all idle detection state.\n", rtDir, store.BinDir)
	}

	return b.String()
}

// runtimeDir returns the base path where runtime files live for this sandbox.
// Container backends use /yoloai; host-filesystem backends (seatbelt) use the
// host sandbox dir; VM backends that declare VMRuntimeDir use that path
// (e.g. Tart uses /Users/admin/.yoloai, the symlinked path with no spaces).
func runtimeDir(sandboxDir string, meta *store.Environment) string {
	if meta.HostFilesystem {
		return sandboxDir
	}
	if desc, ok := runtime.Descriptor(meta.BackendType); ok && desc.Capabilities.VMRuntimeDir != "" {
		return desc.Capabilities.VMRuntimeDir
	}
	return "/yoloai"
}

// writeDir writes a single directory line to the builder.
func writeDir(b *strings.Builder, mountPath, hostPath string, mode store.DirMode, isWorkdir bool) {
	b.WriteString("- ")
	b.WriteString(mountPath)

	if mountPath != hostPath {
		fmt.Fprintf(b, " → %s", hostPath)
	}

	b.WriteString(" (")
	b.WriteString(string(mode))
	b.WriteString(")")

	if isWorkdir {
		b.WriteString(" ← working directory")
	}

	b.WriteString("\n")
}

// WriteContextFiles writes the sandbox context file and optional per-agent
// instruction file into the sandbox directory. The agent's native context
// filename comes from EnvSpec.ContextFile (compiled at the orchestrator
// boundary), keeping this assembler agent-agnostic.
func WriteContextFiles(sandboxDir string, meta *store.Environment, spec EnvSpec) error {
	content := GenerateContext(sandboxDir, meta)

	// Write context.md at sandbox root (reference copy)
	contextPath := filepath.Join(sandboxDir, "context.md")
	if err := fileutil.WriteFile(contextPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("write context.md: %w", err)
	}

	// Write full context inline into the agent's native instruction file.
	if spec.ContextFile != "" && spec.HasStateDir {
		refPath := filepath.Join(sandboxDir, store.AgentRuntimeDir, spec.ContextFile)

		// Append, don't clobber (D92): the seed/agent_files stage runs BEFORE this
		// in the create flow, so the user's own context file (e.g. a seeded
		// ~/.claude/CLAUDE.md) may already exist here. Overwriting it would destroy
		// the user's instructions; instead the yoloAI orientation is appended after
		// it (separated by a blank line when the seeded file is non-empty). When no
		// file was seeded, this creates it fresh — byte-identical to the old write.
		sep := ""
		if info, statErr := os.Stat(refPath); statErr == nil && info.Size() > 0 {
			sep = "\n\n"
		}
		if err := appendToFile(refPath, sep+content); err != nil {
			return fmt.Errorf("write agent context file %s: %w", spec.ContextFile, err)
		}

		// Append the yoloAI file-exchange Q&A protocol to the agent's context
		// file so the agent knows how to ask questions through the exchange
		// directory. The protocol is agent-AGNOSTIC — it describes a yoloAI
		// mechanism (question.json/answer.json), keyed only on the agent's
		// declared ContextFile, so every agent that reads a native context file
		// gets it (not just Claude's CLAUDE.md). The fuller DEF-fan-in delivered
		// per the agent's injection method is the envsetup re-homing (D91/D92).
		filesDir := runtimeDir(sandboxDir, meta) + "/files"
		qa := "\n## yoloAI File Exchange Protocol\n\n" +
			"You are running inside a yoloAI sandbox. A file exchange directory is\n" +
			"available at `" + filesDir + "/` — readable and writable from both inside\n" +
			"and outside the sandbox.\n\n" +
			"**When you need to ask a question or need input to continue:**\n\n" +
			"1. Write your question to `" + filesDir + "/question.json`:\n" +
			"   ```json\n" +
			"   {\"question\": \"your question here\", \"context\": \"optional context\"}\n" +
			"   ```\n" +
			"2. Poll `" + filesDir + "/answer.json` every 5 seconds until it appears.\n" +
			"3. Read the answer and continue your task.\n\n" +
			"Do not make assumptions about blocking decisions. Write the question file\n" +
			"and wait. The question will be seen and answered by an external agent or user.\n"
		if err := appendToFile(refPath, qa); err != nil {
			return fmt.Errorf("append Q&A protocol to %s: %w", spec.ContextFile, err)
		}
	}

	return nil
}

// appendToFile appends s to the file at path, creating it (0600) if absent.
func appendToFile(path, s string) error {
	f, err := fileutil.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // path is a sandbox-controlled agent-runtime path
	if err != nil {
		return err
	}
	if _, werr := f.WriteString(s); werr != nil {
		_ = f.Close() //nolint:errcheck // returning the write error
		return werr
	}
	return f.Close()
}
