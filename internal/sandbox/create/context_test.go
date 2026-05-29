// ABOUTME: Tests for sandbox context file generation and writing.
package create

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	_ "github.com/kstenerud/yoloai/internal/runtime/tart" // registers tart descriptor for VMRuntimeDir test
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

func TestGenerateContext_AllFields(t *testing.T) {
	meta := &store.Meta{
		Workdir: store.WorkdirMeta{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		},
		Directories: []store.DirMeta{
			{HostPath: "/opt/lib", MountPath: "/home/user/lib", Mode: "ro"},
			{HostPath: "/data/shared", MountPath: "/data/shared", Mode: "rw"},
		},
		NetworkMode:  "isolated",
		NetworkAllow: []string{"api.anthropic.com", "sentry.io"},
		Resources:    &config.ResourceLimits{CPUs: "4", Memory: "8g"},
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)
	assertContextContains(t, result)
}

// assertContextContains checks all expected sections are present in the context output.
func assertContextContains(t *testing.T, result string) {
	t.Helper()
	checks := []struct {
		substr string
		label  string
	}{
		{"# Sandbox Environment", "missing header"},
		{"yoloAI sandbox", "missing sandbox description"},
		{"## Directories", "missing Directories section"},
		{"/home/user/project (copy) ← working directory", "missing workdir line"},
		{"/home/user/lib → /opt/lib (ro)", "missing aux dir with mount path redirect"},
		{"/data/shared (rw)", "missing rw aux dir"},
		{"## Files", "missing Files section"},
		{"/yoloai/files/", "missing files exchange path"},
		{"## Cache", "missing Cache section"},
		{"/yoloai/cache/", "missing cache path"},
		{"## Terminology", "missing Terminology section"},
		{"## Network", "missing Network section"},
		{"Isolated. Allowed domains: api.anthropic.com, sentry.io", "missing network domains"},
		{"## Resources", "missing Resources section"},
		{"4 cpus, 8g memory", "missing resource limits"},
	}
	for _, c := range checks {
		if !strings.Contains(result, c.substr) {
			t.Error(c.label)
		}
	}
}

func TestGenerateContext_MinimalFields(t *testing.T) {
	meta := &store.Meta{
		Workdir: store.WorkdirMeta{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		},
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)

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
	meta := &store.Meta{
		Workdir: store.WorkdirMeta{
			HostPath:  "/project",
			MountPath: "/project",
			Mode:      "copy",
		},
		NetworkMode: "none",
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)

	if !strings.Contains(result, "No network access.") {
		t.Error("missing 'no network' message")
	}
}

func TestGenerateContext_WorkdirMountPath(t *testing.T) {
	meta := &store.Meta{
		Workdir: store.WorkdirMeta{
			HostPath:  "/host/project",
			MountPath: "/container/project",
			Mode:      "copy",
		},
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)

	if !strings.Contains(result, "/container/project → /host/project (copy) ← working directory") {
		t.Errorf("expected mount path redirect, got:\n%s", result)
	}
}

func TestGenerateContext_SeatbeltFilesPath(t *testing.T) {
	meta := &store.Meta{
		Name:           "test-sb",
		Backend:        "seatbelt",
		HostFilesystem: true,
		Workdir: store.WorkdirMeta{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		},
	}

	sandboxDir := "/tmp/yoloai-test-sb/test-sb"
	result := GenerateContext(sandboxDir, meta)

	expectedFilesPath := filepath.Join(sandboxDir, "files") + "/"
	if !strings.Contains(result, expectedFilesPath) {
		t.Errorf("expected seatbelt files path %q in context, got:\n%s", expectedFilesPath, result)
	}
	if strings.Contains(result, "/yoloai/files/") {
		t.Error("seatbelt context should not contain /yoloai/files/")
	}
	expectedCachePath := filepath.Join(sandboxDir, "cache") + "/"
	if !strings.Contains(result, expectedCachePath) {
		t.Errorf("expected seatbelt cache path %q in context, got:\n%s", expectedCachePath, result)
	}
	if strings.Contains(result, "/yoloai/cache/") {
		t.Error("seatbelt context should not contain /yoloai/cache/")
	}
}

func TestGenerateContext_DockerFilesPath(t *testing.T) {
	meta := &store.Meta{
		Name:    "test-sb",
		Backend: "docker",
		Workdir: store.WorkdirMeta{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		},
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)

	if !strings.Contains(result, "/yoloai/files/") {
		t.Error("docker context should contain /yoloai/files/")
	}
	if !strings.Contains(result, "/yoloai/cache/") {
		t.Error("docker context should contain /yoloai/cache/")
	}
}

func TestGenerateContext_TartFilesPath(t *testing.T) {
	meta := &store.Meta{
		Name:    "test-sb",
		Backend: "tart",
		Workdir: store.WorkdirMeta{
			HostPath:  "/Users/admin/project",
			MountPath: "/Users/admin/project",
			Mode:      "copy",
		},
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)

	if !strings.Contains(result, "/Users/admin/.yoloai/files/") {
		t.Errorf("tart context should contain /Users/admin/.yoloai/files/, got:\n%s", result)
	}
	if !strings.Contains(result, "/Users/admin/.yoloai/cache/") {
		t.Errorf("tart context should contain /Users/admin/.yoloai/cache/, got:\n%s", result)
	}
	if strings.Contains(result, "/yoloai/files/") {
		t.Error("tart context should not contain /yoloai/files/")
	}
	if strings.Contains(result, "/yoloai/cache/") {
		t.Error("tart context should not contain /yoloai/cache/")
	}
}

func TestWriteContextFiles_WritesContextAndRef(t *testing.T) {
	sandboxDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750); err != nil {
		t.Fatal(err)
	}

	meta := &store.Meta{
		Workdir: store.WorkdirMeta{
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
	refData, err := os.ReadFile(filepath.Join(sandboxDir, store.AgentRuntimeDir, "CLAUDE.md")) //nolint:gosec // G304: test helper path
	if err != nil {
		t.Fatalf("read agent instruction file: %v", err)
	}
	if !strings.Contains(string(refData), "# Sandbox Environment") {
		t.Error("agent instruction file missing inlined context")
	}
}

func TestWriteContextFiles_NoRefWhenEmpty(t *testing.T) {
	sandboxDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750); err != nil {
		t.Fatal(err)
	}

	meta := &store.Meta{
		Workdir: store.WorkdirMeta{
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
	entries, _ := os.ReadDir(filepath.Join(sandboxDir, store.AgentRuntimeDir))
	for _, e := range entries {
		t.Errorf("unexpected file in %s: %s", store.AgentRuntimeDir, e.Name())
	}
}
