// ABOUTME: Tests for the SystemClient discovery verbs — static agent/backend
// ABOUTME: catalogs and the opt-in backend availability probe.

package yoloai

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemClient_Agents_Catalog(t *testing.T) {
	c := newTestClient(t)

	agents := c.Agents(AgentQuery{})
	require.Len(t, agents, len(agent.AllAgentNames()), "one AgentInfo per shipped agent")

	byName := make(map[string]AgentInfo, len(agents))
	for _, a := range agents {
		byName[a.Name] = a
	}
	claude, ok := byName["claude"]
	require.True(t, ok, "claude agent present in catalog")
	assert.NotEmpty(t, claude.Description)
	assert.Contains(t, []string{"interactive", "headless"}, claude.PromptMode)
}

func TestSystemClient_Agents_RealOnly(t *testing.T) {
	c := newTestClient(t)

	real := make(map[string]bool)
	for _, a := range c.Agents(AgentQuery{RealOnly: true}) {
		real[a.Name] = true
	}
	for _, pseudo := range []string{"test", "shell", "idle"} {
		assert.False(t, real[pseudo], "RealOnly excludes pseudo-agent %q", pseudo)
	}
	assert.True(t, real["claude"], "RealOnly keeps real agents")
	assert.Less(t, len(real), len(c.Agents(AgentQuery{})), "RealOnly returns fewer than the full catalog")
}

func TestSystemClient_Backends_StaticCatalog(t *testing.T) {
	c := newTestClient(t)

	backends := c.Backends(context.Background(), BackendQuery{})
	descs := runtime.Descriptors()
	require.Len(t, backends, len(descs), "one BackendInfo per registered backend")

	for i, b := range backends {
		assert.Equal(t, descs[i].Name, b.Name, "registration order preserved")
		assert.NotEmpty(t, b.Description)
		// Without a probe, availability is never asserted.
		assert.False(t, b.Available)
		assert.Empty(t, b.Note)
	}
}

func TestSystemClient_Backends_Probed(t *testing.T) {
	c := newTestClient(t)

	backends := c.Backends(context.Background(), BackendQuery{ProbeAvailability: true})
	require.Len(t, backends, len(runtime.Descriptors()))
	for _, b := range backends {
		if !b.Available {
			assert.NotEmpty(t, b.Note, "an unavailable backend must explain why")
		}
	}
}
