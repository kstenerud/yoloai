// ABOUTME: Tests for sandbox context file generation and writing.
package envsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	_ "github.com/kstenerud/yoloai/internal/runtime/tart" // registers tart descriptor for VMRuntimeDir test
	"github.com/kstenerud/yoloai/internal/store"
)

func TestGenerateContext_AllFields(t *testing.T) {
	meta := &store.Environment{
		Dirs: []store.DirEnvironment{
			{HostPath: "/home/user/project", MountPath: "/home/user/project", Mode: "copy"},
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
	meta := &store.Environment{
		Dirs: []store.DirEnvironment{{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		}},
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
	meta := &store.Environment{
		Dirs: []store.DirEnvironment{{
			HostPath:  "/project",
			MountPath: "/project",
			Mode:      "copy",
		}},
		NetworkMode: "none",
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)

	if !strings.Contains(result, "No network access.") {
		t.Error("missing 'no network' message")
	}
}

func TestGenerateContext_WorkdirMountPath(t *testing.T) {
	meta := &store.Environment{
		Dirs: []store.DirEnvironment{{
			HostPath:  "/host/project",
			MountPath: "/container/project",
			Mode:      "copy",
		}},
	}

	result := GenerateContext("/tmp/yoloai-test-sb/test-sb", meta)

	if !strings.Contains(result, "/container/project → /host/project (copy) ← working directory") {
		t.Errorf("expected mount path redirect, got:\n%s", result)
	}
}

func TestGenerateContext_SeatbeltFilesPath(t *testing.T) {
	meta := &store.Environment{
		Name:           "test-sb",
		BackendType:    "seatbelt",
		HostFilesystem: true,
		Dirs: []store.DirEnvironment{{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		}},
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
	meta := &store.Environment{
		Name:        "test-sb",
		BackendType: "docker",
		Dirs: []store.DirEnvironment{{
			HostPath:  "/home/user/project",
			MountPath: "/home/user/project",
			Mode:      "copy",
		}},
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
	meta := &store.Environment{
		Name:        "test-sb",
		BackendType: "tart",
		Dirs: []store.DirEnvironment{{
			HostPath:  "/Users/admin/project",
			MountPath: "/Users/admin/project",
			Mode:      "copy",
		}},
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

	meta := &store.Environment{
		Dirs: []store.DirEnvironment{{
			HostPath:  "/project",
			MountPath: "/project",
			Mode:      "copy",
		}},
	}
	spec := EnvSpec{ContextFile: "CLAUDE.md", HasStateDir: true}

	if err := WriteContextFiles(sandboxDir, meta, spec); err != nil {
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
	if !strings.Contains(string(refData), "yoloAI File Exchange Protocol") {
		t.Error("agent instruction file missing the Q&A file-exchange protocol")
	}
}

// TestWriteContextFiles_QAProtocolIsAgentAgnostic verifies the file-exchange Q&A
// protocol is appended to ANY agent's context file (keyed on the declared
// ContextFile), not just Claude's CLAUDE.md — the agnosticism fix.
func TestWriteContextFiles_QAProtocolIsAgentAgnostic(t *testing.T) {
	sandboxDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750); err != nil {
		t.Fatal(err)
	}

	meta := &store.Environment{
		Dirs: []store.DirEnvironment{{HostPath: "/project", MountPath: "/project", Mode: "copy"}},
	}
	spec := EnvSpec{ContextFile: "GEMINI.md", HasStateDir: true}

	if err := WriteContextFiles(sandboxDir, meta, spec); err != nil {
		t.Fatalf("WriteContextFiles: %v", err)
	}

	refData, err := os.ReadFile(filepath.Join(sandboxDir, store.AgentRuntimeDir, "GEMINI.md")) //nolint:gosec // G304: test helper path
	if err != nil {
		t.Fatalf("read GEMINI.md: %v", err)
	}
	if !strings.Contains(string(refData), "# Sandbox Environment") {
		t.Error("GEMINI.md missing inlined context")
	}
	if !strings.Contains(string(refData), "yoloAI File Exchange Protocol") {
		t.Error("GEMINI.md missing the Q&A protocol — it must be agent-agnostic, not Claude-only")
	}
}

func TestWriteContextFiles_NoRefWhenEmpty(t *testing.T) {
	sandboxDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750); err != nil {
		t.Fatal(err)
	}

	meta := &store.Environment{
		Dirs: []store.DirEnvironment{{
			HostPath:  "/project",
			MountPath: "/project",
			Mode:      "copy",
		}},
	}
	spec := EnvSpec{} // no context file, no state dir

	if err := WriteContextFiles(sandboxDir, meta, spec); err != nil {
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

// TestWriteContextFiles_AppendsNotClobbers verifies that when the agent's
// context file was already seeded (e.g. the user's ~/.claude/CLAUDE.md copied in
// via agent_files), the yoloAI orientation is APPENDED, not overwritten (D92).
func TestWriteContextFiles_AppendsNotClobbers(t *testing.T) {
	sandboxDir := t.TempDir()
	runtimeDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	if err := os.MkdirAll(runtimeDir, 0750); err != nil {
		t.Fatal(err)
	}
	// Pre-seed a user context file at the agent-runtime path.
	const userMarker = "MY OWN PROJECT INSTRUCTIONS — do not lose me"
	if err := os.WriteFile(filepath.Join(runtimeDir, "CLAUDE.md"), []byte(userMarker+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	meta := &store.Environment{
		Dirs: []store.DirEnvironment{{HostPath: "/project", MountPath: "/project", Mode: "copy"}},
	}
	spec := EnvSpec{ContextFile: "CLAUDE.md", HasStateDir: true}

	if err := WriteContextFiles(sandboxDir, meta, spec); err != nil {
		t.Fatalf("WriteContextFiles: %v", err)
	}

	refData, err := os.ReadFile(filepath.Join(runtimeDir, "CLAUDE.md")) //nolint:gosec // G304: test helper path
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	got := string(refData)
	if !strings.Contains(got, userMarker) {
		t.Error("the user's seeded context was clobbered — it must be preserved")
	}
	if !strings.Contains(got, "# Sandbox Environment") {
		t.Error("the yoloAI orientation was not appended")
	}
	if !strings.Contains(got, "yoloAI File Exchange Protocol") {
		t.Error("the Q&A protocol was not appended")
	}
	// The user's content must come BEFORE the yoloAI orientation.
	if strings.Index(got, userMarker) > strings.Index(got, "# Sandbox Environment") {
		t.Error("the yoloAI orientation must be appended after the user's content")
	}
}
