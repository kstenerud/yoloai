// ABOUTME: Guards the D144 line-2 launch-time credential disclosure: it must
// ABOUTME: fire when a declared key resolves from the host env, and stay silent
// ABOUTME: otherwise, writing to state.State.Output either way (never a panic on
// ABOUTME: the nil Output several callers construct).

package launch

import (
	"bytes"
	"testing"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/stretchr/testify/assert"
)

func TestDiscloseInjectedCredentials_WritesLineWhenKeyResolved(t *testing.T) {
	var buf bytes.Buffer
	st := &state.State{
		Notices: feedback.WriterSink(&buf),
		Layout:  config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"}),
	}
	spec := envsetup.EnvSpec{AgentName: "claude", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}}

	discloseInjectedCredentials(st, spec)

	assert.Equal(t, "credentials injected from the environment: ANTHROPIC_API_KEY (declared by agent \"claude\")\n", buf.String())
}

func TestDiscloseInjectedCredentials_SilentWhenNothingResolved(t *testing.T) {
	var buf bytes.Buffer
	st := &state.State{
		Notices: feedback.WriterSink(&buf),
		Layout:  config.Layout{},
	}
	spec := envsetup.EnvSpec{AgentName: "claude", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}}

	discloseInjectedCredentials(st, spec)

	assert.Empty(t, buf.String(), "no key resolved must produce no output")
}

// TestDiscloseInjectedCredentials_UnsetSinkIsAProgrammingError inverts what
// this test used to assert.
//
// It previously checked that a nil Output did not panic, because outputOr
// silently substituted io.Discard. That default is exactly what D145 removes:
// a leaf that quietly absorbs a missing destination makes "the caller wanted
// silence" and "the wiring is broken" the same observable, and the second one
// then ships. The guarantee a real caller actually needs — that constructing a
// Client without sinks yields working discards rather than a crash — lives at
// the edge where it belongs, in TestNewClient_WithoutSinksDiscards.
func TestDiscloseInjectedCredentials_UnsetSinkIsAProgrammingError(t *testing.T) {
	st := &state.State{
		Layout: config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"}),
	}
	spec := envsetup.EnvSpec{AgentName: "claude", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}}

	assert.PanicsWithValue(t,
		"feedback: nil Sink; pass feedback.Discard to drop notices deliberately",
		func() { discloseInjectedCredentials(st, spec) },
		"a credential disclosure with nowhere to go must fail loudly — it is the D144 grant record")
}
