// ABOUTME: Tests for the System discovery verbs — static agent/backend
// ABOUTME: catalogs and the opt-in backend availability probe.

package yoloai

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystem_Agents_Catalog(t *testing.T) {
	c := newTestClient(t)

	agents := c.AgentTypes(AgentQuery{})
	require.Len(t, agents, len(agent.AllAgentTypes()), "one AgentInfo per shipped agent")

	byName := make(map[string]AgentInfo, len(agents))
	for _, a := range agents {
		byName[string(a.Type)] = a
	}
	claude, ok := byName["claude"]
	require.True(t, ok, "claude agent present in catalog")
	assert.NotEmpty(t, claude.Description)
	assert.Contains(t, []string{"interactive", "headless"}, claude.PromptMode)
	// Capability flags an embedder uses to choose how to drive the agent.
	assert.True(t, claude.IdleHook, "claude declares an authoritative turn hook (tier-2 idle)")
	assert.True(t, claude.SupportsHeadless, "claude supports a headless/one-shot launch form")
	assert.True(t, claude.SupportsResume, "claude declares native conversation-resume (--continue)")
	assert.NotEmpty(t, claude.NetworkFloor, "claude declares a network floor (its required API domains)")

	// SupportsResume is a per-agent capability, not universal: an agent without a
	// native resume flag reports false (the catalog distinguishes them).
	test, ok := byName["test"]
	require.True(t, ok, "test pseudo-agent present in catalog")
	assert.False(t, test.SupportsResume, "the test agent declares no native-resume flag")
}

func TestSystem_Agents_RealOnly(t *testing.T) {
	c := newTestClient(t)

	real := make(map[string]bool)
	for _, a := range c.AgentTypes(AgentQuery{RealOnly: true}) {
		real[string(a.Type)] = true
	}
	for _, pseudo := range []string{"test", "shell", "idle"} {
		assert.False(t, real[pseudo], "RealOnly excludes pseudo-agent %q", pseudo)
	}
	assert.True(t, real["claude"], "RealOnly keeps real agents")
	assert.Less(t, len(real), len(c.AgentTypes(AgentQuery{})), "RealOnly returns fewer than the full catalog")
}

func TestSystem_Archetypes(t *testing.T) {
	c := newTestClient(t)

	names := c.Archetypes()
	require.NotEmpty(t, names, "at least one shipped archetype")
	for i := 1; i < len(names); i++ {
		assert.LessOrEqual(t, names[i-1], names[i], "archetype names are sorted")
	}
}

func TestSystem_Backends_StaticCatalog(t *testing.T) {
	c := newTestClient(t)

	backends := c.BackendTypes(context.Background(), BackendQuery{})
	descs := runtime.Descriptors()
	require.Len(t, backends, len(descs), "one BackendInfo per registered backend")

	for i, b := range backends {
		assert.Equal(t, descs[i].Type, b.Type, "registration order preserved")
		assert.NotEmpty(t, b.Description)
		// Without a probe, availability is never asserted.
		assert.False(t, b.Available)
		assert.Empty(t, b.Note)
	}
}

func TestSystem_Backends_Probed(t *testing.T) {
	c := newTestClient(t)

	backends := c.BackendTypes(context.Background(), BackendQuery{ProbeAvailability: true})
	require.Len(t, backends, len(runtime.Descriptors()))
	for _, b := range backends {
		if !b.Available {
			assert.NotEmpty(t, b.Note, "an unavailable backend must explain why")
		}
	}
}

func TestSystem_CheckBackend_Unknown(t *testing.T) {
	c := newTestClient(t)

	available, note := c.CheckBackend(context.Background(), "does-not-exist")
	assert.False(t, available, "an unregistered backend is never available")
	assert.NotEmpty(t, note, "an unavailable backend explains why")
}
