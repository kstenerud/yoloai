// ABOUTME: compose_test.go tests the domain-allowlist composition functions:
// ABOUTME: AgentFloor (agent domain set), WithProvenance (provenance tagging),
// ABOUTME: and Compose (mode + allowlist resolution).

package netpolicy_test

import (
	"testing"

	"github.com/kstenerud/yoloai/internal/netpolicy"
	"github.com/stretchr/testify/assert"
)

func TestAgentFloor(t *testing.T) {
	t.Run("known agent returns non-empty map containing expected domain", func(t *testing.T) {
		floor := netpolicy.AgentFloor("claude")
		assert.NotNil(t, floor)
		assert.True(t, floor["api.anthropic.com"], "expected api.anthropic.com in claude floor")
	})

	t.Run("unknown agent returns empty non-nil map", func(t *testing.T) {
		floor := netpolicy.AgentFloor("ghost")
		assert.NotNil(t, floor)
		assert.Empty(t, floor)
	})
}

func TestWithProvenance(t *testing.T) {
	t.Run("empty allow list returns empty non-nil slice", func(t *testing.T) {
		result := netpolicy.WithProvenance(nil, "claude")
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("tags agent-floor domain as agent-requirement and user domain as user", func(t *testing.T) {
		allow := []string{"api.anthropic.com", "extra.example"}
		result := netpolicy.WithProvenance(allow, "claude")
		assert.Len(t, result, 2)
		assert.Equal(t, netpolicy.AllowedDomain{
			Domain: "api.anthropic.com",
			Source: netpolicy.AllowedFromAgentRequirement,
		}, result[0])
		assert.Equal(t, netpolicy.AllowedDomain{
			Domain: "extra.example",
			Source: netpolicy.AllowedFromUser,
		}, result[1])
	})

	t.Run("unknown agent tags all domains as user", func(t *testing.T) {
		allow := []string{"api.anthropic.com", "extra.example"}
		result := netpolicy.WithProvenance(allow, "ghost")
		for _, d := range result {
			assert.Equal(t, netpolicy.AllowedFromUser, d.Source)
		}
	})
}

func TestCompose(t *testing.T) {
	t.Run("default mode returns empty string and nil allow", func(t *testing.T) {
		mode, allow := netpolicy.Compose("", []string{"a.example"}, []string{"b.example"})
		assert.Equal(t, "", mode)
		assert.Nil(t, allow)
	})

	t.Run("none mode returns none and nil allow", func(t *testing.T) {
		mode, allow := netpolicy.Compose("none", []string{"a.example"}, []string{"b.example"})
		assert.Equal(t, "none", mode)
		assert.Nil(t, allow)
	})

	t.Run("isolated mode concatenates agent floor and user allow", func(t *testing.T) {
		mode, allow := netpolicy.Compose("isolated", []string{"api.anthropic.com"}, []string{"extra.example"})
		assert.Equal(t, "isolated", mode)
		assert.Equal(t, []string{"api.anthropic.com", "extra.example"}, allow)
	})

	t.Run("isolated mode with empty lists", func(t *testing.T) {
		mode, allow := netpolicy.Compose("isolated", nil, nil)
		assert.Equal(t, "isolated", mode)
		// append of two nil slices yields nil — match current buildNetworkConfig behavior
		assert.Nil(t, allow)
	})
}
