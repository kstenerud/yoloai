// ABOUTME: Unit tests for the S3 orchestration re-route: verifies that
// ABOUTME: Launch-capable backends take the keepalive+Launch path and non-capable
// ABOUTME: backends take the unchanged legacy path, with correct ordering.
package launch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// --- call-recording fake runtime (base, no ProcessLauncher) ---

// rerouteBaseRuntime records Create/Start/Inspect calls and returns running=true
// on Inspect so verifyInstanceRunning is satisfied.
type rerouteBaseRuntime struct {
	mu         sync.Mutex
	callSeq    []string                // ordered call log: "Create", "Start", "Ready", "Launch", "Inspect"
	createdCfg *runtime.InstanceConfig // the InstanceConfig passed to Create (for env assertions)
}

var _ runtime.Backend = (*rerouteBaseRuntime)(nil)

func (r *rerouteBaseRuntime) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callSeq = append(r.callSeq, name)
}

func (r *rerouteBaseRuntime) Create(_ context.Context, cfg runtime.InstanceConfig) error {
	r.record("Create")
	r.mu.Lock()
	copied := cfg
	r.createdCfg = &copied
	r.mu.Unlock()
	return nil
}
func (r *rerouteBaseRuntime) Start(_ context.Context, _ string) error {
	r.record("Start")
	return nil
}
func (r *rerouteBaseRuntime) Inspect(_ context.Context, _ string) (runtime.InstanceInfo, error) {
	r.record("Inspect")
	return runtime.InstanceInfo{Running: true}, nil
}
func (r *rerouteBaseRuntime) Stop(_ context.Context, _ string) error   { return nil }
func (r *rerouteBaseRuntime) Remove(_ context.Context, _ string) error { return nil }
func (r *rerouteBaseRuntime) Exec(_ context.Context, _ string, _ []string, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (r *rerouteBaseRuntime) GitExec(_ context.Context, _ string, _ string, _ string, _ ...string) (string, error) {
	return "", nil
}
func (r *rerouteBaseRuntime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string, _ runtime.IOStreams) error {
	return nil
}
func (r *rerouteBaseRuntime) Setup(_ context.Context, _ config.Layout, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
	return nil
}
func (r *rerouteBaseRuntime) IsReady(_ context.Context) (bool, error) { return true, nil }
func (r *rerouteBaseRuntime) Close() error                            { return nil }
func (r *rerouteBaseRuntime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, nil
}
func (r *rerouteBaseRuntime) Logs(_ context.Context, _ string, _ int) string { return "" }
func (r *rerouteBaseRuntime) DiagHint(name string) string                    { return "check: " + name }
func (r *rerouteBaseRuntime) TmuxSocket(_ string) string                     { return "" }
func (r *rerouteBaseRuntime) AttachCommand(_ string, _, _ int, _ runtime.IsolationMode) []string {
	return nil
}
func (r *rerouteBaseRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "mock",
		BaseModeName: runtime.IsolationModeContainer,
		Capabilities: runtime.BackendCaps{
			NetworkIsolation: true,
			CapAdd:           true,
			// This fake represents a Docker-like backend that opts into the D88
			// keepalive-holder + Launch bring-up; these tests verify that path.
			AgentFreeLaunch: true,
		},
	}
}

// --- fake Process returned by the Launch-capable backend ---

// fakeProcess simulates a launched process. On creation it writes the
// .secrets-consumed marker into sandboxLogsDir so waitForSecretsConsumed
// observes it promptly (mirrors what sandbox-setup.py does in the real system).
type fakeProcess struct {
	sandboxLogsDir string
}

func (p *fakeProcess) ID() string { return "fake-exec-id" }
func (p *fakeProcess) Streams() runtime.ProcessStreams {
	return runtime.ProcessStreams{
		Stdout: strings.NewReader(""),
		Stderr: strings.NewReader(""),
	}
}
func (p *fakeProcess) Wait(_ context.Context) (runtime.ExitStatus, error) {
	return runtime.ExitStatus{Code: 0}, nil
}

// --- Launch-capable backend (embeds base + adds ProcessLauncher) ---

// rerouteLaunchRuntime embeds rerouteBaseRuntime and also implements
// runtime.ProcessLauncher. It records the ProcSpec it received and writes the
// .secrets-consumed marker (simulating sandbox-setup.py) so the ordering
// assertion can verify that waitForSecretsConsumed runs after Launch.
type rerouteLaunchRuntime struct {
	rerouteBaseRuntime
	launchedSpec  *runtime.ProcSpec // set on first Launch call
	launchedCname string            // container name passed to Launch
	markerPath    string            // set before Launch; written by Launch to simulate the runner
}

var _ runtime.ProcessLauncher = (*rerouteLaunchRuntime)(nil)

// Ready reports the substrate ready immediately (the entrypoint-provisioning
// step is simulated as instantaneous). It records the call so tests can assert
// the readiness gate runs between Start and Launch.
func (r *rerouteLaunchRuntime) Ready(_ context.Context, _ string) (bool, error) {
	r.record("Ready")
	return true, nil
}

func (r *rerouteLaunchRuntime) Launch(_ context.Context, name string, spec runtime.ProcSpec) (runtime.Process, error) {
	r.record("Launch")
	r.mu.Lock()
	copied := spec
	r.launchedSpec = &copied
	r.launchedCname = name
	r.mu.Unlock()

	// Simulate sandbox-setup.py writing the secrets-consumed marker.
	if r.markerPath != "" {
		dir := filepath.Dir(r.markerPath)
		_ = os.MkdirAll(dir, 0750)
		_ = os.WriteFile(r.markerPath, nil, 0600)
	}

	return &fakeProcess{sandboxLogsDir: filepath.Dir(r.markerPath)}, nil
}

// --- helpers ---

// writeMinimalRuntimeConfig writes a minimal valid runtime-config.json into sandboxDir.
func writeMinimalRuntimeConfig(t *testing.T, sandboxDir string) {
	t.Helper()
	cfg := runtimeconfig.ContainerConfig{
		SchemaVersion: runtimeconfig.SchemaVersion,
		AgentCommand:  "claude",
		WorkingDir:    "/project",
		SandboxName:   "test-sandbox",
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), data, 0600))
}

// makeTestState builds a minimal *state.State for launch tests. sandboxDir must
// already exist.
func makeTestState(sandboxDir string) *state.State {
	return &state.State{
		Name:       "test-sandbox",
		SandboxDir: sandboxDir,
		Workdir: &state.DirSpec{
			Path: "/project",
			Mode: store.DirMode("copy"),
		},
		Agent:     agent.GetAgent("test"),
		ImageRef:  "yoloai-base:test",
		Layout:    config.NewLayout(filepath.Join(sandboxDir, ".yoloai")),
		Isolation: runtime.IsolationModeContainer,
	}
}

// readRuntimeConfigKeepalive reads keepalive_only from the runtime-config.json
// in sandboxDir.
func readRuntimeConfigKeepalive(t *testing.T, sandboxDir string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // G304: path is test-controlled temp dir
	require.NoError(t, err)
	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))
	return cfg.KeepaliveOnly
}

// --- tests ---

// TestBuildAndStart_LaunchPath verifies the new-flow (Launch-capable backend):
//
//   - runtime-config.json ends with keepalive_only: true
//   - Launch is called with the expected ProcSpec (sandbox-setup.py, user=yoloai)
//   - the secrets-consumed marker wait occurs AFTER Launch (verified by call order
//     in the recorded sequence: Create → Start → Launch, then marker visible)
//   - verifyInstanceRunning succeeds (Inspect returns Running=true)
func TestBuildAndStart_LaunchPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "logs"), 0750))

	writeMinimalRuntimeConfig(t, dir)
	markerPath := filepath.Join(dir, store.SecretsConsumedMarker)

	rt := &rerouteLaunchRuntime{}
	rt.markerPath = markerPath

	st := makeTestState(dir)
	err := buildAndStart(context.Background(), rt, st, nil, nil, true /*hasSecrets*/, nil, brokerOutcome{})
	require.NoError(t, err)

	// keepalive_only must be set in the on-disk config.
	assert.True(t, readRuntimeConfigKeepalive(t, dir),
		"keepalive_only must be true after the Launch path sets it")

	// keepalive_only must ALSO be signalled via the container env, the channel that
	// survives Docker Desktop's stale single-file-bind-mount-after-rename (the file
	// patch alone never reaches the entrypoint there). See backend-idiosyncrasies.md.
	rt.mu.Lock()
	createdCfg := rt.createdCfg
	rt.mu.Unlock()
	require.NotNil(t, createdCfg, "Create must have been called")
	assert.Contains(t, createdCfg.ContainerEnv, "YOLOAI_KEEPALIVE_ONLY=1",
		"the Launch path must set YOLOAI_KEEPALIVE_ONLY=1 in the container env")

	// Launch must have been called.
	rt.mu.Lock()
	spec := rt.launchedSpec
	rt.mu.Unlock()
	require.NotNil(t, spec, "Launch must have been called once")

	// ProcSpec must point at sandbox-setup.py (via sh -c wrapper), run as yoloai, and be detached.
	assert.Equal(t, "yoloai", spec.User, "ProcSpec.User must be yoloai")
	assert.True(t, spec.Detached, "ProcSpec.Detached must be true for the session-runner")
	joined := strings.Join(spec.Argv, " ")
	assert.Contains(t, joined, "sandbox-setup.py", "ProcSpec.Argv must reference sandbox-setup.py")
	assert.Contains(t, joined, "session-runner.log", "ProcSpec.Argv must redirect output to session-runner.log")

	// Call order: Create → Start → Ready → Launch. The readiness gate must run
	// between Start and Launch (a runner launched before the box is ready is
	// killed mid-setup — DF44).
	rt.mu.Lock()
	seq := append([]string(nil), rt.callSeq...)
	rt.mu.Unlock()

	createIdx := indexOf(seq, "Create")
	startIdx := indexOf(seq, "Start")
	readyIdx := indexOf(seq, "Ready")
	launchIdx := indexOf(seq, "Launch")
	require.NotEqual(t, -1, createIdx, "Create must be called")
	require.NotEqual(t, -1, startIdx, "Start must be called")
	require.NotEqual(t, -1, readyIdx, "Ready must be called")
	require.NotEqual(t, -1, launchIdx, "Launch must be called")
	assert.Less(t, createIdx, startIdx, "Create must precede Start")
	assert.Less(t, startIdx, readyIdx, "Start must precede the readiness gate")
	assert.Less(t, readyIdx, launchIdx, "the readiness gate must precede Launch")

	// The marker must be present (fakeProcess wrote it; the wait observed it).
	_, statErr := os.Stat(markerPath)
	assert.NoError(t, statErr, "secrets-consumed marker must exist after Launch")
}

// TestBuildAndStart_ContainerEnhancedTakesLegacyPath verifies that a
// Launch-capable, AgentFreeLaunch-opted-in backend is STILL routed to the legacy
// in-entrypoint weld under container-enhanced (gVisor) isolation. The D88
// keepalive+Launch path's host-side `exec --user yoloai` resolves the username
// against gVisor's stale image passwd and writes as the wrong UID, so the agent
// never welds (see runtime.SupportsAgentFreeLaunch). The legacy path must be used
// instead: no keepalive_only patch and no Launch call.
func TestBuildAndStart_ContainerEnhancedTakesLegacyPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "logs"), 0750))
	writeMinimalRuntimeConfig(t, dir)

	rt := &rerouteLaunchRuntime{}

	st := makeTestState(dir)
	st.Isolation = runtime.IsolationModeContainerEnhanced

	// hasSecrets=false so the legacy path doesn't block on the secrets marker
	// (the entrypoint would write it in production; the fake doesn't simulate it).
	err := buildAndStart(context.Background(), rt, st, nil, nil, false /*hasSecrets*/, nil, brokerOutcome{})
	require.NoError(t, err)

	// Legacy path: keepalive_only stays false and Launch is never called.
	assert.False(t, readRuntimeConfigKeepalive(t, dir),
		"container-enhanced must NOT patch keepalive_only (legacy weld)")
	rt.mu.Lock()
	spec := rt.launchedSpec
	seq := append([]string(nil), rt.callSeq...)
	rt.mu.Unlock()
	assert.Nil(t, spec, "Launch must NOT be called under container-enhanced (legacy path)")
	assert.Equal(t, -1, indexOf(seq, "Launch"), "the call sequence must not contain Launch")
	assert.NotEqual(t, -1, indexOf(seq, "Create"), "Create must still be called")
	assert.NotEqual(t, -1, indexOf(seq, "Start"), "Start must still be called")
}

// TestBuildAndStart_LegacyPath verifies the unchanged legacy flow for backends
// without ProcessLauncher:
//
//   - runtime-config.json is NOT patched with keepalive_only: true
//   - Launch is never called
//   - Create → Start happen in order; Inspect returns Running=true
func TestBuildAndStart_LegacyPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "logs"), 0750))

	writeMinimalRuntimeConfig(t, dir)

	rt := &rerouteBaseRuntime{} // no ProcessLauncher

	st := makeTestState(dir)
	// No secrets in this test — simplifies the legacy path (no marker wait).
	err := buildAndStart(context.Background(), rt, st, nil, nil, false /*hasSecrets*/, nil, brokerOutcome{})
	require.NoError(t, err)

	// keepalive_only must NOT have been set.
	assert.False(t, readRuntimeConfigKeepalive(t, dir),
		"keepalive_only must remain false for the legacy path")

	// Create and Start must both have been called; Launch must not appear.
	rt.mu.Lock()
	seq := append([]string(nil), rt.callSeq...)
	rt.mu.Unlock()

	assert.Contains(t, seq, "Create", "Create must be called in legacy path")
	assert.Contains(t, seq, "Start", "Start must be called in legacy path")
	assert.NotContains(t, seq, "Launch", "Launch must NOT be called in legacy path")

	// The legacy path must NOT set the keepalive env var — the entrypoint welds the
	// agent inline and would otherwise be forced into the holder branch.
	rt.mu.Lock()
	createdCfg := rt.createdCfg
	rt.mu.Unlock()
	require.NotNil(t, createdCfg, "Create must have been called")
	assert.NotContains(t, createdCfg.ContainerEnv, "YOLOAI_KEEPALIVE_ONLY=1",
		"the legacy path must NOT set YOLOAI_KEEPALIVE_ONLY")

	createIdx := indexOf(seq, "Create")
	startIdx := indexOf(seq, "Start")
	assert.Less(t, createIdx, startIdx, "Create must precede Start in legacy path")
}

// TestBuildAndStart_LaunchPath_NoSecrets verifies that in the Launch path with
// no secrets injected (hasSecrets=false) the flow still works: keepalive_only is
// patched, Launch is called, no marker wait is attempted.
func TestBuildAndStart_LaunchPath_NoSecrets(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "logs"), 0750))

	writeMinimalRuntimeConfig(t, dir)

	rt := &rerouteLaunchRuntime{}
	// No markerPath — if we accidentally call waitForSecretsConsumed it would
	// spin for the full timeout; leaving it empty ensures the test is fast.
	rt.markerPath = ""

	st := makeTestState(dir)
	err := buildAndStart(context.Background(), rt, st, nil, nil, false /*hasSecrets*/, nil, brokerOutcome{})
	require.NoError(t, err)

	assert.True(t, readRuntimeConfigKeepalive(t, dir),
		"keepalive_only must still be set even when there are no secrets")

	rt.mu.Lock()
	spec := rt.launchedSpec
	rt.mu.Unlock()
	require.NotNil(t, spec, "Launch must have been called even with no secrets")
}

// indexOf returns the index of the first occurrence of s in seq, or -1.
func indexOf(seq []string, s string) int {
	for i, v := range seq {
		if v == s {
			return i
		}
	}
	return -1
}
