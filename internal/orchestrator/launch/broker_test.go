// ABOUTME: Unit tests for applyBrokerEnv — the pure secretEnv rewrite that swaps
// ABOUTME: the real API key for a base_url + placeholder pointing at the injector.
package launch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/runtime"
)

func claudeBrokerConfig() *agent.BrokerConfig {
	return &agent.BrokerConfig{ //nolint:gosec // G101 false positive: env-var NAMES + a placeholder, not real credentials
		UpstreamURL:     "https://api.anthropic.com",
		Destination:     "api.anthropic.com",
		Header:          "x-api-key",
		APIKeyEnvVar:    "ANTHROPIC_API_KEY",
		BaseURLEnvVar:   "ANTHROPIC_BASE_URL",
		AuthTokenEnvVar: "ANTHROPIC_AUTH_TOKEN",
		DummyToken:      "yoloai-broker-dummy",
	}
}

func TestApplyBrokerEnv_LinuxGatewayForBoth(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key", "OTHER": "keep"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	require.NoError(t, applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "172.17.0.1:44115"))

	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey, "real key removed from the container env")
	assert.Equal(t, "http://172.17.0.1:44115", secretEnv["ANTHROPIC_BASE_URL"])
	assert.Equal(t, "yoloai-broker-dummy", secretEnv["ANTHROPIC_AUTH_TOKEN"])
	assert.Equal(t, "keep", secretEnv["OTHER"], "unrelated secrets are untouched")
}

func TestApplyBrokerEnv_DialHostDiffersFromBindHost(t *testing.T) {
	// Docker Desktop: the injector binds the mac host loopback but the agent
	// reaches it via host.docker.internal. base_url must use DialHost + the bound port.
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key"}
	reach := runtime.InjectorReach{BindHost: "127.0.0.1", DialHost: "host.docker.internal"}

	require.NoError(t, applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "127.0.0.1:51000"))

	assert.Equal(t, "http://host.docker.internal:51000", secretEnv["ANTHROPIC_BASE_URL"])
	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey)
}

func TestApplyBrokerEnv_BadInjectorAddr(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "not-an-addr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse injector address")
	// On error the real key must remain (we did not partially rewrite into a leak).
	assert.Equal(t, "the-real-key", secretEnv["ANTHROPIC_API_KEY"])
}
