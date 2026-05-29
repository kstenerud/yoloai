// ABOUTME: Tests for resolveAndApplyArchetype: CLI flag, .yoloai.yaml, and auto-detection priority.
// ABOUTME: Covers devcontainer expansion, compose expansion, and transparency output suppression.

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/archetype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestEngine builds a Engine for resolution tests. Layout is rooted at
// t.TempDir() so the Engine satisfies Q-W.5's WithLayout-required invariant
// without test runs leaking sandbox dirs into the repo working copy. The
// Engine holds no output writer (F8); tests that want to capture create-pipeline
// progress pass a writer via CreateOptions.Output.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	return NewEngine(&mockDockerRuntime{}, slog.Default(), strings.NewReader("y\n"),
		WithLayout(config.NewLayout(t.TempDir())))
}

// makeWorkdir creates a temp dir suitable as a sandbox workdir.
func makeWorkdir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// --- Priority tests ---

func TestResolveArchetype_CLIFlagOverridesAll(t *testing.T) {
	dir := makeWorkdir(t)
	// Plant a .yoloai.yaml with a different archetype
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte("archetype: compose\n"), 0600))
	// Plant a compose file too (auto-detect would pick compose)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{
		Workdir:   DirSpec{Path: dir},
		Archetype: "simple", // CLI overrides
	}
	pr := &profileResult{}

	arch, dc, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeSimple, arch)
	assert.Nil(t, dc)
}

func TestResolveArchetype_YamlOverridesAutoDetect(t *testing.T) {
	dir := makeWorkdir(t)
	// Plant a .yoloai.yaml declaring simple (overriding what auto-detect would find)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte("archetype: simple\n"), 0600))
	// Plant a compose file (auto-detect would pick compose)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{
		Workdir: DirSpec{Path: dir},
	}
	pr := &profileResult{}

	arch, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeSimple, arch)
}

func TestResolveArchetype_AutoDetectSimple(t *testing.T) {
	dir := makeWorkdir(t)
	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	arch, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeSimple, arch)
}

func TestResolveArchetype_AutoDetectCompose(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	arch, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeCompose, arch)
	// Should have set container-privileged isolation and dockerd required
	assert.Equal(t, runtime.IsolationModeContainerPrivileged, opts.Isolation)
	assert.True(t, pr.archetypeDockerDRequired)
}

func TestResolveArchetype_AutoDetectDevcontainer(t *testing.T) {
	dir := makeWorkdir(t)
	dcDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(dcDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
		"name": "test",
		"forwardPorts": [3000]
	}`), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	arch, dc, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeDevcontainer, arch)
	require.NotNil(t, dc)
	assert.Equal(t, []int{3000}, dc.ForwardPorts)
	// Ports should be merged into opts
	assert.Contains(t, opts.Ports, "3000:3000")
}

// --- Devcontainer expansion ---

func TestResolveArchetype_DevcontainerMergesEnv(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{
		"containerEnv": {"FOO": "bar", "EXISTING": "old"},
		"remoteEnv": {"FOO": "remote"}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{
		Workdir: DirSpec{Path: dir},
	}
	pr := &profileResult{env: map[string]string{"EXISTING": "user-set"}}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	// remoteEnv wins over containerEnv for FOO
	assert.Equal(t, "remote", pr.env["FOO"])
	// pr.env existing key wins over devcontainer env
	assert.Equal(t, "user-set", pr.env["EXISTING"])
}

func TestResolveArchetype_DevcontainerWorkspaceFolder(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{"workspaceFolder": "/workspace/myproject"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, "/workspace/myproject", opts.Workdir.MountPath)
}

func TestResolveArchetype_DevcontainerDockerComposeFileErrors(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{"dockerComposeFile": "docker-compose.yml"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Compose devcontainers are not supported")
}

func TestResolveArchetype_DevcontainerFiltersMounts(t *testing.T) {
	dir := makeWorkdir(t)
	safePath := t.TempDir()
	dcContent := fmt.Sprintf(`{
		"mounts": [
			"/var/run/docker.sock:/var/run/docker.sock",
			"%s:/container/safe:ro"
		]
	}`, safePath)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, dcMounts, warnings, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	// Docker socket filtered out, safe path passes through
	assert.Len(t, dcMounts, 1)
	assert.Contains(t, dcMounts[0], safePath)
	assert.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "docker socket")
}

func TestResolveArchetype_DevcontainerPostStartCompose(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{"postStartCommand": "docker compose up -d"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, runtime.IsolationModeContainerPrivileged, opts.Isolation)
	assert.True(t, pr.archetypeDockerDRequired)
}

// --- Transparency output ---

func TestResolveArchetype_TransparencyOutput_Simple(t *testing.T) {
	dir := makeWorkdir(t)
	var buf bytes.Buffer
	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}, Output: &buf}
	pr := &profileResult{}

	arch, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeSimple, arch)
	// Simple + auto-detected → no transparency output
	assert.Empty(t, buf.String())
}

func TestResolveArchetype_TransparencyOutput_CLIFlag(t *testing.T) {
	dir := makeWorkdir(t)
	var buf bytes.Buffer
	m := newTestEngine(t)
	opts := &CreateOptions{
		Workdir:   DirSpec{Path: dir},
		Archetype: "simple",
		Output:    &buf,
	}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	// CLI flag → should print "→ --archetype simple"
	assert.Contains(t, buf.String(), "--archetype simple")
}

func TestResolveArchetype_TransparencyOutput_Compose(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	var buf bytes.Buffer
	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}, Output: &buf}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	output := buf.String()
	// Should contain archetype and suppression hint
	assert.Contains(t, output, "compose")
	assert.Contains(t, output, "--archetype simple")
}

// --- .yoloai.yaml mounts merged ---

func TestResolveArchetype_YamlMountsMerged(t *testing.T) {
	dir := makeWorkdir(t)
	content := "mounts:\n  - /data:/container/data:ro\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Contains(t, pr.mounts, "/data:/container/data:ro")
}

func TestResolveArchetype_YamlMountsDeduped(t *testing.T) {
	dir := makeWorkdir(t)
	content := "mounts:\n  - /data:/container/data:ro\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	// Pre-existing mount already in pr.mounts
	pr := &profileResult{mounts: []string{"/data:/container/data:ro"}}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	// Should not duplicate
	count := 0
	for _, m := range pr.mounts {
		if m == "/data:/container/data:ro" {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

// --- requires: validation ---

func TestResolveArchetype_Requires_WarnsButDoesNotBlock(t *testing.T) {
	dir := makeWorkdir(t)
	content := "requires:\n  yoloai: \">=99.0\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	// requires: version verification is unimplemented, so the constraint is a
	// non-blocking warning — creation must proceed regardless (no prompt, no error).
	var buf bytes.Buffer
	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}, Output: &buf}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "version verification not yet implemented")
}

// --- RunArgs expansion ---

func TestResolveArchetype_DevcontainerRunArgs_CPUMemory(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{"runArgs": ["--cpus", "4", "--memory", "8g"]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, pr)
	require.NoError(t, err)
	require.NotNil(t, pr.resources)
	assert.Equal(t, "4", pr.resources.CPUs)
	assert.Equal(t, "8g", pr.resources.Memory)
}

// --- Per-call Output routing (F8) ---

// TestCreateOutput_PerCallWriterReceivesAdvisories verifies that a create-pipeline
// advisory routes to CreateOptions.Output. The Engine holds no output writer of
// its own (F8), so this per-call writer is the only sink — concurrent Creates can
// keep their progress streams separate.
func TestCreateOutput_PerCallWriterReceivesAdvisories(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"),
		[]byte("archetype: simple\nrequires:\n  foo: \">=1\"\n"), 0600))

	var callBuf bytes.Buffer
	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}, Output: &callBuf}

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, &profileResult{})
	require.NoError(t, err)

	assert.Contains(t, callBuf.String(), "version verification not yet implemented",
		"the requires: advisory must reach the per-call writer")
}

// TestCreateOutput_NilWriterIsDiscarded verifies the documented contract: a nil
// CreateOptions.Output is resolved to io.Discard, so the pipeline runs silently
// without panicking on a nil io.Writer. (The yoloai.Client seeds Output from its
// Options.Output, so a nil here means a direct library caller opted out.)
func TestCreateOutput_NilWriterIsDiscarded(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"),
		[]byte("archetype: simple\nrequires:\n  foo: \">=1\"\n"), 0600))

	m := newTestEngine(t)
	opts := &CreateOptions{Workdir: DirSpec{Path: dir}} // Output left nil → io.Discard

	_, _, _, _, err := m.resolveAndApplyArchetype(context.Background(), opts, &profileResult{})
	require.NoError(t, err)
}

// --- Lifecycle command to JSON ---

func TestLifecycleCmdToJSON_String(t *testing.T) {
	var cmd archetype.LifecycleCmd
	require.NoError(t, json.Unmarshal([]byte(`"npm install"`), &cmd))
	result := lifecycleCmdToJSON(cmd)
	assert.Equal(t, "string", result["type"])
	assert.Equal(t, "npm install", result["cmd"])
}

func TestLifecycleCmdToJSON_Array(t *testing.T) {
	var cmd archetype.LifecycleCmd
	require.NoError(t, json.Unmarshal([]byte(`["go", "mod", "download"]`), &cmd))
	result := lifecycleCmdToJSON(cmd)
	assert.Equal(t, "array", result["type"])
	assert.Equal(t, []string{"go", "mod", "download"}, result["cmd"])
}

func TestLifecycleCmdToJSON_Object(t *testing.T) {
	var cmd archetype.LifecycleCmd
	require.NoError(t, json.Unmarshal([]byte(`{"step1": "make build"}`), &cmd))
	result := lifecycleCmdToJSON(cmd)
	assert.Equal(t, "object", result["type"])
	obj, ok := result["cmd"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "make build", obj["step1"])
}
