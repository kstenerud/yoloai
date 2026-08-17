// ABOUTME: Tests for resolveAndApplyArchetype: CLI flag vs auto-detection priority, and that a
// ABOUTME: stale .yoloai.yaml (D140) is ignored but warned about. Covers devcontainer expansion,
// ABOUTME: compose expansion, and transparency output suppression.

package create

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/archetype"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDeps builds state.Deps for resolution tests. Layout is rooted at
// t.TempDir() so tests don't leak sandbox dirs into the repo working copy.
func newTestDeps(t *testing.T) state.Deps {
	t.Helper()
	return state.Deps{
		Runtime: &mockDockerRuntime{},
		Layout:  config.NewLayout(t.TempDir()),
		Input:   strings.NewReader("y\n"),
	}
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

	d := newTestDeps(t)
	opts := &Options{
		Workdir:   DirSpec{Path: dir},
		Archetype: "simple", // CLI overrides
	}
	pr := &profileResult{}

	arch, dc, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeSimple, arch)
	assert.Nil(t, dc)
}

// TestResolveArchetype_YamlArchetypeIgnored is the D140 revert-red test for the
// archetype: key removal: .yoloai.yaml is no longer read at all, so an
// archetype: declaration in it must not override auto-detection.
func TestResolveArchetype_YamlArchetypeIgnored(t *testing.T) {
	dir := makeWorkdir(t)
	// Plant a .yoloai.yaml declaring compose (would have overridden pre-D140)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte("archetype: compose\n"), 0600))
	// Plant devcontainer signals — auto-detection must pick devcontainer regardless.
	dcDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(dcDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"name": "test"}`), 0600))

	d := newTestDeps(t)
	opts := &Options{
		Workdir: DirSpec{Path: dir},
	}
	pr := &profileResult{}

	arch, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeDevcontainer, arch)
}

func TestResolveArchetype_AutoDetectSimple(t *testing.T) {
	dir := makeWorkdir(t)
	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	arch, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeSimple, arch)
}

func TestResolveArchetype_AutoDetectCompose(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	arch, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeCompose, arch)
	// Security: an auto-detected compose file must NOT auto-escalate to
	// container-privileged (full host access). The user must opt in explicitly.
	assert.NotEqual(t, runtime.IsolationModeContainerPrivileged, opts.Isolation)
	assert.False(t, pr.archetypeDockerDRequired)
}

func TestResolveArchetype_ComposeExplicitPrivilegedEnablesDockerd(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	d := newTestDeps(t)
	// User explicitly opted into container-privileged → DinD is set up.
	opts := &Options{Workdir: DirSpec{Path: dir}, Isolation: runtime.IsolationModeContainerPrivileged}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
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

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	arch, dc, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
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

	d := newTestDeps(t)
	opts := &Options{
		Workdir: DirSpec{Path: dir},
	}
	pr := &profileResult{env: map[string]string{"EXISTING": "user-set"}}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
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

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Equal(t, "/workspace/myproject", opts.Workdir.MountPath)
}

func TestResolveArchetype_DevcontainerDockerComposeFileErrors(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{"dockerComposeFile": "docker-compose.yml"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Compose devcontainers are not supported")
}

// TestResolveProfileAndArchetype_DevcontainerMountsStillWork is the D142
// regression guard: removing the config/profile mounts: key must not touch
// devcontainer.json's own mounts:, which stays governed by D141 and flows
// through the full pipeline (resolveProfileAndArchetype, not just the lower
// archetype-resolution step) into the merged mount list.
func TestResolveProfileAndArchetype_DevcontainerMountsStillWork(t *testing.T) {
	dir := makeWorkdir(t)
	safePath := t.TempDir()
	dcContent := fmt.Sprintf(`{"mounts": ["%s:/container/safe:ro"]}`, safePath)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	var agentDef *agent.Definition
	ycfg := &config.YoloaiConfig{}
	gcfg := &config.GlobalConfig{}

	ri, err := resolveProfileAndArchetype(context.Background(), d, opts, agentDef, ycfg, gcfg)
	require.NoError(t, err)
	require.Len(t, ri.mergedMounts, 1)
	assert.Contains(t, ri.mergedMounts[0], safePath)
	assert.Contains(t, ri.mergedMounts[0], "/container/safe:ro")
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

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, dcMounts, notices, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	// Docker socket filtered out, safe path passes through
	assert.Len(t, dcMounts, 1)
	assert.Contains(t, dcMounts[0], safePath)
	require.Len(t, notices, 1)
	assert.Equal(t, "devcontainer.mount_stripped", notices[0].Event)
	assert.Equal(t, "docker_socket", notices[0].Fields["reason"])
}

func TestResolveArchetype_DevcontainerPostStartCompose(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{"postStartCommand": "docker compose up -d"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	// Security: a repo's postStartCommand compose usage must NOT auto-escalate
	// to container-privileged without explicit user opt-in.
	assert.NotEqual(t, runtime.IsolationModeContainerPrivileged, opts.Isolation)
	assert.False(t, pr.archetypeDockerDRequired)
}

// --- Transparency output ---

func TestResolveArchetype_TransparencyOutput_Simple(t *testing.T) {
	dir := makeWorkdir(t)
	var buf bytes.Buffer
	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}, Output: &buf}
	pr := &profileResult{}

	arch, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Equal(t, archetype.ArchetypeSimple, arch)
	// Simple + auto-detected → no transparency output
	assert.Empty(t, buf.String())
}

func TestResolveArchetype_TransparencyOutput_CLIFlag(t *testing.T) {
	dir := makeWorkdir(t)
	var buf bytes.Buffer
	d := newTestDeps(t)
	opts := &Options{
		Workdir:   DirSpec{Path: dir},
		Archetype: "simple",
		Output:    &buf,
	}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	// CLI flag → should print "→ --archetype simple"
	assert.Contains(t, buf.String(), "--archetype simple")
}

func TestResolveArchetype_TransparencyOutput_Compose(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	var buf bytes.Buffer
	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}, Output: &buf}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	output := buf.String()
	// Should contain archetype and suppression hint
	assert.Contains(t, output, "compose")
	assert.Contains(t, output, "--archetype simple")
}

// --- .yoloai.yaml is no longer read (D140) ---

// TestResolveArchetype_YamlMountsNotAdded is the D140 revert-red test for the
// mounts: key removal. This is the security-relevant behavior change:
// .yoloai.yaml mounts bypassed FilterMounts entirely (unlike devcontainer.json
// mounts, which are filtered — docker socket, credential dirs, workdir
// collisions stripped). The file must no longer be read at all, so a mounts:
// entry must not reach pr.mounts.
func TestResolveArchetype_YamlMountsNotAdded(t *testing.T) {
	dir := makeWorkdir(t)
	content := "mounts:\n  - /data:/container/data:ro\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Empty(t, pr.mounts)
}

// TestResolveArchetype_YamlPresenceWarns is the D140 revert-red test for the
// existence-check warning: a workdir with a .yoloai.yaml must produce a warning
// that the file is no longer read, so a repo relying on mounts: learns its host
// mounts are gone instead of losing them silently.
func TestResolveArchetype_YamlPresenceWarns(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte("archetype: simple\n"), 0600))

	var buf bytes.Buffer
	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}, Output: &buf}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), ".yoloai.yaml is no longer read")
}

// --- RunArgs expansion ---

func TestResolveArchetype_DevcontainerRunArgs_CPUMemory(t *testing.T) {
	dir := makeWorkdir(t)
	dcContent := `{"runArgs": ["--cpus", "4", "--memory", "8g"]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(dcContent), 0600))

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}}
	pr := &profileResult{}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, pr)
	require.NoError(t, err)
	require.NotNil(t, pr.resources)
	assert.Equal(t, "4", pr.resources.CPUs)
	assert.Equal(t, "8g", pr.resources.Memory)
}

// --- Per-call Output routing (F8) ---

// TestCreateOutput_PerCallWriterReceivesAdvisories verifies that a create-pipeline
// advisory routes to Options.Output.
func TestCreateOutput_PerCallWriterReceivesAdvisories(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"),
		[]byte("archetype: simple\n"), 0600))

	var callBuf bytes.Buffer
	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}, Output: &callBuf}

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, &profileResult{})
	require.NoError(t, err)

	assert.Contains(t, callBuf.String(), ".yoloai.yaml is no longer read",
		"the .yoloai.yaml-present advisory must reach the per-call writer")
}

// TestCreateOutput_NilWriterIsDiscarded verifies the documented contract: a nil
// Options.Output is resolved to io.Discard, so the pipeline runs silently
// without panicking on a nil io.Writer.
func TestCreateOutput_NilWriterIsDiscarded(t *testing.T) {
	dir := makeWorkdir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"),
		[]byte("archetype: simple\n"), 0600))

	d := newTestDeps(t)
	opts := &Options{Workdir: DirSpec{Path: dir}} // Output left nil → io.Discard

	_, _, _, _, err := resolveAndApplyArchetype(context.Background(), d, opts, &profileResult{})
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
