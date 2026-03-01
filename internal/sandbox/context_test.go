// ABOUTME: Tests for sandbox context file generation and writing.
package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
)

func TestGenerateContext_AllFields(t *testing.T) {
	meta := &Meta{
		Workdir: WorkdirMeta{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		},
		Directories: []DirMeta{
			{HostPath: "/opt/lib", MountPath: "/home/user/lib", Mode: "ro"},
			{HostPath: "/data/shared", MountPath: "/data/shared", Mode: "rw"},
		},
		NetworkMode:  "isolated",
		NetworkAllow: []string{"api.anthropic.com", "sentry.io"},
		Resources:    &ResourceLimits{CPUs: "4", Memory: "8g"},
	}

	result := GenerateContext(meta)

	// Header
	if !strings.Contains(result, "# Sandbox Environment") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "yoloAI sandbox") {
		t.Error("missing sandbox description")
	}

	// Directories
	if !strings.Contains(result, "## Directories") {
		t.Error("missing Directories section")
	}
	if !strings.Contains(result, "/home/user/project (copy) ← working directory") {
		t.Error("missing workdir line")
	}
	if !strings.Contains(result, "/home/user/lib → /opt/lib (ro)") {
		t.Error("missing aux dir with mount path redirect")
	}
	if !strings.Contains(result, "/data/shared (rw)") {
		t.Error("missing rw aux dir")
	}

	// Network
	if !strings.Contains(result, "## Network") {
		t.Error("missing Network section")
	}
	if !strings.Contains(result, "Isolated. Allowed domains: api.anthropic.com, sentry.io") {
		t.Error("missing network domains")
	}

	// Resources
	if !strings.Contains(result, "## Resources") {
		t.Error("missing Resources section")
	}
	if !strings.Contains(result, "4 cpus, 8g memory") {
		t.Error("missing resource limits")
	}
}

func TestGenerateContext_MinimalFields(t *testing.T) {
	meta := &Meta{
		Workdir: WorkdirMeta{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		},
	}

	result := GenerateContext(meta)

	if !strings.Contains(result, "## Directories") {
		t.Error("missing Directories section")
	}
	if strings.Contains(result, "## Network") {
		t.Error("Network section should be omitted when no network mode set")
	}
	if strings.Contains(result, "## Resources") {
		t.Error("Resources section should be omitted when no resources set")
	}
}

func TestGenerateContext_NetworkNone(t *testing.T) {
	meta := &Meta{
		Workdir: WorkdirMeta{
			HostPath:  "/project",
			MountPath: "/project",
			Mode:      "copy",
		},
		NetworkMode: "none",
	}

	result := GenerateContext(meta)

	if !strings.Contains(result, "No network access.") {
		t.Error("missing 'no network' message")
	}
}

func TestGenerateContext_WorkdirMountPath(t *testing.T) {
	meta := &Meta{
		Workdir: WorkdirMeta{
			HostPath:  "/host/project",
			MountPath: "/container/project",
			Mode:      "copy",
		},
	}

	result := GenerateContext(meta)

	if !strings.Contains(result, "/container/project → /host/project (copy) ← working directory") {
		t.Errorf("expected mount path redirect, got:\n%s", result)
	}
}

func TestWriteContextFiles_WritesContextAndRef(t *testing.T) {
	sandboxDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750); err != nil {
		t.Fatal(err)
	}

	meta := &Meta{
		Workdir: WorkdirMeta{
			HostPath:  "/project",
			MountPath: "/project",
			Mode:      "copy",
		},
	}
	agentDef := &agent.Definition{
		Name:        "claude",
		StateDir:    "/home/yoloai/.claude/",
		ContextFile: "CLAUDE.md",
	}

	if err := WriteContextFiles(sandboxDir, meta, agentDef); err != nil {
		t.Fatalf("WriteContextFiles: %v", err)
	}

	// Check context.md exists and has content
	contextData, err := os.ReadFile(filepath.Join(sandboxDir, "context.md")) //nolint:gosec // G304: test helper path
	if err != nil {
		t.Fatalf("read context.md: %v", err)
	}
	if !strings.Contains(string(contextData), "# Sandbox Environment") {
		t.Error("context.md missing header")
	}

	// Check agent instruction file contains full context (inlined, not a pointer)
	refData, err := os.ReadFile(filepath.Join(sandboxDir, "agent-state", "CLAUDE.md")) //nolint:gosec // G304: test helper path
	if err != nil {
		t.Fatalf("read agent instruction file: %v", err)
	}
	if !strings.Contains(string(refData), "# Sandbox Environment") {
		t.Error("agent instruction file missing inlined context")
	}
}

func TestWriteContextFiles_NoRefWhenEmpty(t *testing.T) {
	sandboxDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750); err != nil {
		t.Fatal(err)
	}

	meta := &Meta{
		Workdir: WorkdirMeta{
			HostPath:  "/project",
			MountPath: "/project",
			Mode:      "copy",
		},
	}
	agentDef := &agent.Definition{
		Name:     "test",
		StateDir: "",
	}

	if err := WriteContextFiles(sandboxDir, meta, agentDef); err != nil {
		t.Fatalf("WriteContextFiles: %v", err)
	}

	// context.md should exist
	if _, err := os.Stat(filepath.Join(sandboxDir, "context.md")); err != nil {
		t.Error("context.md should be created")
	}

	// No agent ref file should be created
	entries, _ := os.ReadDir(filepath.Join(sandboxDir, "agent-state"))
	for _, e := range entries {
		t.Errorf("unexpected file in agent-state: %s", e.Name())
	}
}
