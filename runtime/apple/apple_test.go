// ABOUTME: Apple (Tart VM) backend registration, probe status, and the
// ABOUTME: macOS host routing that prefers apple over the container backends.
package apple

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entryJSON renders one `container list --format json` entry with the given name
// and labels — the shape the real CLI emits (verified against container 1.0.0:
// labels ride at configuration.labels and survive the sandbox dir's deletion).
func entryJSON(name string, labels map[string]string) string {
	lbl, err := json.Marshal(labels)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`{"id":%q,"configuration":{"id":%q,"labels":%s}}`, name, name, lbl)
}

func yoloaiLabels(principal, sandbox string) map[string]string {
	return map[string]string{
		runtime.LabelSandbox:   sandbox,
		runtime.LabelPrincipal: principal,
	}
}

// TestOrphanInstances locks the Prune sweep's filter: an orphan is a container
// yoloai created (com.yoloai.sandbox present) for THIS principal (principal label
// equal) that isn't in the known set. Identity is the label set, never the name
// prefix (D62, DF115).
func TestOrphanInstances(t *testing.T) {
	list := "[" + strings.Join([]string{
		entryJSON("yoloai-cli-alpha", yoloaiLabels("cli", "alpha")),
		entryJSON("yoloai-cli-beta", yoloaiLabels("cli", "beta")),
		entryJSON("yoloai-cli-gamma", yoloaiLabels("cli", "gamma")),
	}, ",") + "]"

	got, err := orphanInstances(list, "cli", []string{"yoloai-cli-beta"})
	require.NoError(t, err)
	assert.Equal(t, []string{"yoloai-cli-alpha", "yoloai-cli-gamma"}, got, "unknown instances of this principal are orphans")

	// Empty listing is the CLI's real no-containers output, not an error.
	got, err = orphanInstances("[]", "cli", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestOrphanInstances_SparesForeignPrincipal is the case this filter exists for,
// and it must be built deliberately: nothing in a normal run produces a
// cross-principal container, so a green suite proves nothing without it (DF115).
// The name is deliberately inside THIS principal's namespace while the label says
// otherwise — that isolates the predicate from the name, which is the whole point.
// Prefix matching reaps both of these; label equality spares both.
func TestOrphanInstances_SparesForeignPrincipal(t *testing.T) {
	list := "[" + strings.Join([]string{
		entryJSON("yoloai-acme-probe", yoloaiLabels("acme", "probe")),
		entryJSON("yoloai-cli-liar", yoloaiLabels("acme", "liar")),
		entryJSON("yoloai-cli-mine", yoloaiLabels("cli", "mine")),
	}, ",") + "]"

	got, err := orphanInstances(list, "cli", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"yoloai-cli-mine"}, got, "another principal's instances must survive this principal's sweep")
}

// TestOrphanInstances_SparesForeignContainer covers the live win the label
// predicate buys on top of principal scoping: a container merely NAMED yoloai-*
// that yoloai never created (a hand-run `container run --name yoloai-cli-x`)
// carries no com.yoloai.sandbox label. Prefix matching destroys it; label
// equality leaves it alone (runtime.IsOrphanCandidate, D62).
func TestOrphanInstances_SparesForeignContainer(t *testing.T) {
	list := "[" + strings.Join([]string{
		entryJSON("yoloai-cli-handrun", nil),
		entryJSON("yoloai-cli-nolabels", map[string]string{"com.example.other": "x"}),
		entryJSON("yoloai-cli-real", yoloaiLabels("cli", "real")),
	}, ",") + "]"

	got, err := orphanInstances(list, "cli", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"yoloai-cli-real"}, got, "a container yoloai did not create must never be swept")
}

// TestOrphanInstances_MalformedFailsClosed: prune deletes things, so an
// unreadable listing must error rather than silently sweep nothing (or worse,
// fall back to names).
func TestOrphanInstances_MalformedFailsClosed(t *testing.T) {
	_, err := orphanInstances("not json", "cli", nil)
	assert.Error(t, err)
}

// TestRegistered confirms the backend's init() registered a sane descriptor —
// vm-tier, darwin/arm64, with the verified capability flags.
func TestRegistered(t *testing.T) {
	d, ok := runtime.Descriptor(runtime.BackendApple)
	require.True(t, ok, "apple backend must be registered")
	assert.Equal(t, runtime.BackendApple, d.Type)
	assert.Equal(t, runtime.IsolationModeVM, d.BaseModeName, "apple is vm-tier, not container-slot")
	assert.Equal(t, []string{"darwin"}, d.Platforms)
	assert.Equal(t, []string{"arm64"}, d.Architectures)
	assert.True(t, d.Capabilities.NetworkIsolation, "in-guest iptables verified")
	assert.True(t, d.Capabilities.CapAdd)
	require.NotNil(t, d.Probe)
}

// TestProbe_TierOnThisHost checks the probe never returns Running (apple is
// started on demand) and is Absent off macOS/arm64. On a supported host it is
// Absent (CLI/version gate) or Installed — never Running.
func TestProbe_TierOnThisHost(t *testing.T) {
	status, reason := probe(context.Background(), nil)
	assert.NotEqual(t, runtime.ProbeRunning, status, "apple probe reports Installed at most; running is checked at point-of-use")

	if !isMacOS() || !isAppleSilicon() {
		assert.Equal(t, runtime.ProbeAbsent, status)
		assert.NotEmpty(t, reason)
		return
	}
	assert.Contains(t,
		[]runtime.ProbeStatus{runtime.ProbeAbsent, runtime.ProbeInstalled}, status,
		"supported host: Absent (gate) or Installed")
}

// TestSelectBackend_DarwinPrefersApple verifies the macOS-host routing: when
// apple is installed it's the default and the --isolation vm target, but
// --isolation container and an explicit container preference stay in the
// container slot. Runs only on a Mac with the container CLI installed; this test
// binary registers both apple and — via apple's import — docker.
func TestSelectBackend_DarwinPrefersApple(t *testing.T) {
	ctx := context.Background()
	if !isMacOS() {
		t.Skip("apple routing only applies on a macOS host")
	}
	if installed, _ := runtime.Installed(ctx, runtime.BackendApple, nil); !installed {
		t.Skip("apple backend not installed on this host")
	}

	got, _ := runtime.SelectBackend(ctx, "", runtime.IsolationModeDefault, "", nil)
	assert.Equal(t, runtime.BackendApple, got, "macOS default prefers apple")

	got, _ = runtime.SelectBackend(ctx, "", runtime.IsolationModeVM, "", nil)
	assert.Equal(t, runtime.BackendApple, got, "--isolation vm routes to apple")

	got, _ = runtime.SelectBackend(ctx, "", runtime.IsolationModeContainer, "", nil)
	assert.NotEqual(t, runtime.BackendApple, got, "--isolation container is not apple")

	got, _ = runtime.SelectBackend(ctx, runtime.BackendDocker, runtime.IsolationModeDefault, "", nil)
	assert.NotEqual(t, runtime.BackendApple, got, "explicit container_backend wins over the apple default")

	got, _ = runtime.SelectBackend(ctx, runtime.BackendApple, runtime.IsolationModeDefault, "", nil)
	assert.Equal(t, runtime.BackendApple, got, "container_backend=apple is honored")
}
