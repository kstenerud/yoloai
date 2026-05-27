// ABOUTME: Tests for the Q-Y typed-name aliases at the yoloai root package.
// ABOUTME: Verifies that BackendName, AgentName, PruneItemKind, and LogSource
// ABOUTME: preserve the internal type identity and interoperate cleanly at the boundary.

package yoloai

import (
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/stretchr/testify/assert"
)

// BackendName is a type alias of runtime.BackendName, so values flow
// through both names without explicit conversion. Catches a regression
// where a fresh-type (`type BackendName string`, not `=`) gets used
// instead of an alias.
func TestBackendName_AliasIdentity(t *testing.T) {
	// Explicit type annotations are deliberate here even though
	// staticcheck flags them as inferrable. The point of the test
	// IS to exercise the cross-package assignability that an alias
	// provides — eliding the types would make the assertion
	// vacuously hold for a fresh-type definition.
	var public BackendName = BackendDocker //nolint:staticcheck // ST1023: type pins alias identity
	var internal runtime.BackendName       //nolint:staticcheck // ST1023: ditto
	internal = public
	assert.Equal(t, "docker", string(internal))

	// Reverse direction: an internal constant fits into the public type.
	public = runtime.BackendContainerd
	assert.Equal(t, BackendContainerd, public)
}

// Every shipped backend constant matches its internal counterpart's
// string. Regress-guards future divergence (e.g. someone adds a new
// internal constant but forgets the re-export).
func TestBackendName_ShippedConstants(t *testing.T) {
	cases := []struct {
		public BackendName
		want   string
	}{
		{BackendDocker, "docker"},
		{BackendPodman, "podman"},
		{BackendTart, "tart"},
		{BackendSeatbelt, "seatbelt"},
		{BackendContainerd, "containerd"},
	}
	for _, tc := range cases {
		t.Run(string(tc.public), func(t *testing.T) {
			assert.Equal(t, tc.want, string(tc.public))
		})
	}
}

// AgentName parallels BackendName.
func TestAgentName_AliasIdentity(t *testing.T) {
	// See TestBackendName_AliasIdentity for why the type annotations
	// are kept explicit even though staticcheck flags them.
	var public AgentName = AgentClaude //nolint:staticcheck // ST1023: type pins alias identity
	var internal agent.AgentName       //nolint:staticcheck // ST1023: ditto
	internal = public
	assert.Equal(t, "claude", string(internal))

	public = agent.AgentGemini
	assert.Equal(t, AgentGemini, public)
}

func TestAgentName_ShippedConstants(t *testing.T) {
	cases := []struct {
		public AgentName
		want   string
	}{
		{AgentClaude, "claude"},
		{AgentCodex, "codex"},
		{AgentGemini, "gemini"},
		{AgentOpenCode, "opencode"},
		{AgentAider, "aider"},
		{AgentTest, "test"},
	}
	for _, tc := range cases {
		t.Run(string(tc.public), func(t *testing.T) {
			assert.Equal(t, tc.want, string(tc.public))
		})
	}
}

// PruneItemKind is a fresh public type (not an alias) — its underlying
// strings match what the backends actually write into runtime.PruneItem.Kind
// today. If the backend-side spelling drifts, this test surfaces it.
func TestPruneItemKind_Values(t *testing.T) {
	assert.Equal(t, "container", string(PruneKindContainer))
	assert.Equal(t, "image", string(PruneKindImage))
	assert.Equal(t, "vm", string(PruneKindVM))
	assert.Equal(t, "temp_dir", string(PruneKindTempDir))
}

// PruneItem round-trips through its typed fields cleanly — what you
// construct is what you read back. Catches a regression where the
// fields ever stop being string-shaped.
func TestPruneItem_TypedFields(t *testing.T) {
	item := PruneItem{
		Backend: BackendDocker,
		Kind:    PruneKindContainer,
		Name:    "yoloai-mybox",
		Bytes:   1024,
	}
	assert.Equal(t, BackendDocker, item.Backend)
	assert.Equal(t, PruneKindContainer, item.Kind)
	assert.Equal(t, "yoloai-mybox", item.Name)
	assert.Equal(t, int64(1024), item.Bytes)
}

// Options.Backend takes a typed BackendName — the compiler enforces it,
// but a switch over the typed value in a test catches accidental
// "go back to plain string" regressions.
func TestOptions_BackendIsTyped(t *testing.T) {
	opts := Options{Backend: BackendDocker}
	switch opts.Backend {
	case BackendDocker:
		// ok
	case BackendTart, BackendPodman, BackendSeatbelt, BackendContainerd:
		t.Fatalf("unexpected backend matched: %s", opts.Backend)
	default:
		t.Fatalf("backend constant didn't match any shipped value: %s", opts.Backend)
	}
}

func TestRunOptions_AgentIsTyped(t *testing.T) {
	opts := RunOptions{Agent: AgentClaude}
	switch opts.Agent {
	case AgentClaude:
		// ok
	default:
		t.Fatalf("AgentClaude didn't match itself in switch over typed AgentName: %s", opts.Agent)
	}
}

// LogSource is a type alias of store.LogSource, parallel to BackendName /
// AgentName. The same alias-identity test catches a regression where a
// fresh type slips in.
func TestLogSource_AliasIdentity(t *testing.T) {
	// Explicit type annotations are deliberate — they pin the alias
	// identity. See TestBackendName_AliasIdentity for the rationale.
	var public LogSource = LogSourceCLI //nolint:staticcheck // ST1023: type pins alias identity
	var internal store.LogSource        //nolint:staticcheck // ST1023: ditto
	internal = public
	assert.Equal(t, "cli", string(internal))

	public = store.LogSourceMonitor
	assert.Equal(t, LogSourceMonitor, public)
}

func TestLogSource_ShippedConstants(t *testing.T) {
	cases := []struct {
		public LogSource
		want   string
	}{
		{LogSourceCLI, "cli"},
		{LogSourceSandbox, "sandbox"},
		{LogSourceMonitor, "monitor"},
		{LogSourceHooks, "hooks"},
	}
	for _, tc := range cases {
		t.Run(string(tc.public), func(t *testing.T) {
			assert.Equal(t, tc.want, string(tc.public))
		})
	}
}

// MountSpec is a type alias of runtime.MountSpec; values flow through
// both names without explicit conversion. Field names carry the "Path"
// suffix so the call site reads unambiguously even without type
// inference in view (Go doesn't surface the type at every reference).
func TestMountSpec_AliasIdentity(t *testing.T) {
	// Explicit type annotation pins the alias identity. See
	// TestBackendName_AliasIdentity for why this isn't redundant.
	var public MountSpec = MountSpec{HostPath: "/h", ContainerPath: "/c", ReadOnly: true} //nolint:staticcheck // ST1023: type pins alias identity
	var internal runtime.MountSpec
	internal = public
	assert.Equal(t, "/h", internal.HostPath)
	assert.Equal(t, "/c", internal.ContainerPath)
	assert.True(t, internal.ReadOnly)

	// Reverse direction.
	internal = runtime.MountSpec{HostPath: "/x", ContainerPath: "/y"}
	public = internal
	assert.Equal(t, "/x", public.HostPath)
	assert.Equal(t, "/y", public.ContainerPath)
}

// PortMapping is a type alias of runtime.PortMapping; int ports + the
// Port suffix avoid the ambiguity of "Host int" / "Container int".
func TestPortMapping_AliasIdentity(t *testing.T) {
	var public PortMapping = PortMapping{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"} //nolint:staticcheck // ST1023: type pins alias identity
	var internal runtime.PortMapping
	internal = public
	assert.Equal(t, 8080, internal.HostPort)
	assert.Equal(t, 80, internal.ContainerPort)
	assert.Equal(t, "tcp", internal.Protocol)

	internal = runtime.PortMapping{HostPort: 5432, ContainerPort: 5432}
	public = internal
	assert.Equal(t, 5432, public.HostPort)
	assert.Equal(t, 5432, public.ContainerPort)
	assert.Empty(t, public.Protocol, "empty protocol defaults to tcp at the runtime boundary")
}
