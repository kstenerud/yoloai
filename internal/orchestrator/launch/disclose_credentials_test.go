// ABOUTME: Guards the D144 line-2 launch-time credential disclosure: it must
// ABOUTME: fire when a declared key resolves from the host env, and stay silent
// ABOUTME: otherwise, writing to state.State.Output either way (never a panic on
// ABOUTME: the nil Output several callers construct).

package launch

import (
	"bytes"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/stretchr/testify/assert"
)

func TestDiscloseInjectedCredentials_WritesLineWhenKeyResolved(t *testing.T) {
	var buf bytes.Buffer
	st := &state.State{
		Output: &buf,
		Layout: config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"}),
	}
	spec := envsetup.EnvSpec{AgentName: "claude", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}}

	discloseInjectedCredentials(st, spec)

	assert.Equal(t, "credentials injected from the environment: ANTHROPIC_API_KEY (declared by agent \"claude\")\n", buf.String())
}

func TestDiscloseInjectedCredentials_SilentWhenNothingResolved(t *testing.T) {
	var buf bytes.Buffer
	st := &state.State{
		Output: &buf,
		Layout: config.Layout{},
	}
	spec := envsetup.EnvSpec{AgentName: "claude", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}}

	discloseInjectedCredentials(st, spec)

	assert.Empty(t, buf.String(), "no key resolved must produce no output")
}

func TestDiscloseInjectedCredentials_NilOutputDoesNotPanic(t *testing.T) {
	st := &state.State{
		Layout: config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"}),
	}
	spec := envsetup.EnvSpec{AgentName: "claude", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}}

	assert.NotPanics(t, func() { discloseInjectedCredentials(st, spec) })
}
