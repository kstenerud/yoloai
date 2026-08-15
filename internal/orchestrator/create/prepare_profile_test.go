// ABOUTME: Tests for resolveProfileConfig — profile chain resolution and merge.
// ABOUTME: Covers DF207: a profile must merge over baked-in defaults, never the
// ABOUTME: user's personal defaults/config.yaml, and CLI/profile/personal agent
// ABOUTME: precedence.
package create

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProfile writes a profile's config.yaml under layout's profiles dir.
func writeProfile(t *testing.T, layout config.Layout, name, content string) {
	t.Helper()
	dir := layout.ProfileDir(name)
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))
}

// TestResolveProfileConfig_PersonalDefaultsDoNotLeakIntoProfile is the DF207
// headline test. A profile is active; the user's defaults/config.yaml sets
// privilege-bearing and other keys across the board. None of them may reach
// the resolved profileResult/opts — config.md promises profiles are
// self-contained "no exceptions", and the leak found in DF207 was config-wide,
// not limited to the four keys the finding originally named.
func TestResolveProfileConfig_PersonalDefaultsDoNotLeakIntoProfile(t *testing.T) {
	d := newTestDeps(t)
	writeProfile(t, d.Layout, "leaktest", "agent: test\n") // profile sets nothing else

	ycfg := &config.YoloaiConfig{
		Agent:     "claude",
		CapAdd:    []string{"SYS_ADMIN"},
		Devices:   []string{"/dev/personal"},
		Setup:     []string{"echo personal-setup"},
		Isolation: "vm",
		Network: &config.NetworkConfig{
			Allow: []string{"personal.example.com"},
		},
		Env:       map[string]string{"PERSONAL_SECRET": "leak-me-not"},
		AgentArgs: map[string]string{"test": "--personal-flag"},
		Ports:     []string{"9999:9999"},
	}
	gcfg := &config.GlobalConfig{}
	opts := &Options{Name: "sb1", Profile: "leaktest", Agent: "claude"}
	agentDef := agent.GetAgent("claude")

	pr, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
	require.NoError(t, err)

	assert.Empty(t, pr.capAdd, "personal cap_add must not carry into a profile")
	assert.Empty(t, pr.devices, "personal devices must not carry into a profile")
	assert.Empty(t, pr.setup, "personal setup must not carry into a profile")
	assert.NotEqual(t, runtime.IsolationMode("vm"), pr.isolation, "personal isolation must not carry into a profile")
	assert.Empty(t, opts.NetworkAllow, "personal network.allow must not carry into a profile")
	assert.NotContains(t, pr.env, "PERSONAL_SECRET", "personal env must not carry into a profile")
	assert.NotEqual(t, "--personal-flag", pr.agentArgs["test"], "personal agent_args must not carry into a profile")
	assert.Empty(t, opts.Ports, "personal ports must not carry into a profile")
}

// TestArch_ProfileIgnoresPersonalDefaults is the claim cited by config.md:
// "Profiles are self-contained" / "Personal defaults do not carry into
// profiles" (see the TestArch_ prefix convention, AGENTS.md "Preparing a
// PR"). It is broader than TestResolveProfileConfig_PersonalDefaultsDoNotLeakIntoProfile
// above: it drives the full pipeline a profile-active create.Run uses
// (resolveProfileConfig, then applyConfigDefaults) and adds `resources`,
// which the DF207 fix above did not reach — applyBaseResourceDefaults used
// to run unconditionally, so a profile that left resources unset picked up
// the user's personal resources.cpus/memory anyway. That gap is closed in
// the same change as this test (prepare_profile.go's applyConfigDefaults).
//
// `isolation` and `model` are included, and they are the reason this test has
// to simulate the CLI rather than build clean Options. The CLI coalesces
// flag-else-config into one string before the library sees it
// (internal/cli/lifecycle/new.go `resolveNewIsolationOS`,
// internal/cli/cliutil/client.go `ResolveModel`), so a personal isolation/model
// arrives here looking exactly like an explicit --isolation/--model. Options
// below therefore carry the personal values, as the CLI would have produced
// them; asserting against clean Options would pass without proving anything
// (DF209, and this comment previously said so while excluding them).
func TestArch_ProfileIgnoresPersonalDefaults(t *testing.T) {
	d := newTestDeps(t)
	writeProfile(t, d.Layout, "leaktest", "agent: test\n") // profile sets nothing else

	ycfg := &config.YoloaiConfig{
		Agent:     "claude",
		Isolation: "vm",
		Model:     "personal-model",
		CapAdd:    []string{"SYS_ADMIN"},
		Devices:   []string{"/dev/personal"},
		Setup:     []string{"echo personal-setup"},
		Network: &config.NetworkConfig{
			Allow: []string{"personal.example.com"},
		},
		Env:       map[string]string{"PERSONAL_SECRET": "leak-me-not"},
		AgentArgs: map[string]string{"test": "--personal-flag"},
		Ports:     []string{"9999:9999"},
		Resources: &config.ResourceLimits{CPUs: "16", Memory: "64g"},
	}
	gcfg := &config.GlobalConfig{}
	// As the CLI would build them: --agent/--isolation/--model were NOT passed,
	// so each already carries the personal config's value (flag-else-config).
	opts := &Options{
		Name:      "sb-claim-a",
		Profile:   "leaktest",
		Agent:     "claude",
		Isolation: "vm",
		Model:     "personal-model",
	}
	agentDef := agent.GetAgent("claude")

	pr, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
	require.NoError(t, err)
	require.NoError(t, applyConfigDefaults(opts, ycfg, pr, d.Layout.HomeDir, d.Layout.Env().EnvForConfigInterpolation()))

	assert.Empty(t, pr.capAdd, "personal cap_add must not carry into a profile")
	assert.Empty(t, pr.devices, "personal devices must not carry into a profile")
	assert.Empty(t, pr.setup, "personal setup must not carry into a profile")
	assert.Empty(t, opts.NetworkAllow, "personal network.allow must not carry into a profile")
	assert.NotContains(t, pr.env, "PERSONAL_SECRET", "personal env must not carry into a profile")
	assert.NotEqual(t, "--personal-flag", pr.agentArgs["test"], "personal agent_args must not carry into a profile")
	assert.Empty(t, opts.Ports, "personal ports must not carry into a profile")
	// pr.resources is non-nil (the baked-in defaults set resources.cpus/memory
	// to "" rather than omitting the key), so the claim is about its VALUES,
	// not the pointer.
	if pr.resources != nil {
		assert.NotEqual(t, "16", pr.resources.CPUs, "personal resources.cpus must not carry into a profile")
		assert.NotEqual(t, "64g", pr.resources.Memory, "personal resources.memory must not carry into a profile")
	}

	// DF209: the two keys that reach the pipeline pre-coalesced by the CLI.
	assert.NotEqual(t, runtime.IsolationMode("vm"), pr.isolation,
		"a personal isolation: must not override an active profile")
	assert.NotEqual(t, "personal-model", opts.Model,
		"a personal model: must not survive into an active profile")
}

// TestResolveProfileConfig_ProfileOwnValuesStillApply guards against fixing
// the DF207 leak by breaking profiles outright: a profile's own settings must
// still resolve, even though they now merge over baked-in defaults instead of
// the user's config.
func TestResolveProfileConfig_ProfileOwnValuesStillApply(t *testing.T) {
	d := newTestDeps(t)
	writeProfile(t, d.Layout, "selfcontained", `
cap_add:
  - NET_ADMIN
isolation: vm
network:
  allow:
    - profile.example.com
env:
  PROFILE_VAR: profile-value
`)

	ycfg := &config.YoloaiConfig{} // no personal defaults set
	gcfg := &config.GlobalConfig{}
	opts := &Options{Name: "sb2", Profile: "selfcontained", Agent: "claude"}
	agentDef := agent.GetAgent("claude")

	pr, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
	require.NoError(t, err)

	assert.Equal(t, []string{"NET_ADMIN"}, pr.capAdd)
	assert.EqualValues(t, "vm", pr.isolation)
	assert.Contains(t, opts.NetworkAllow, "profile.example.com")
	assert.Equal(t, "profile-value", pr.env["PROFILE_VAR"])
}

// TestResolveProfileConfig_NetworkAllowMergesWhenModeAlreadySet pins DF206 at
// the profile-merge site. --network-allow promotes opts.Network to
// NetworkModeIsolated at the CLI (internal/cli/lifecycle/new.go:197-199)
// before resolveProfileConfig ever runs, so by the time this code sees it the
// mode is already non-default — exactly the sharpest case DF206 named: a user
// who adds one domain on the CLI must not silently lose every domain their
// profile declared. Before the fix, the whole network block (mode promotion
// AND allowlist merge) was gated on opts.Network == NetworkModeDefault, so
// this profile's network.allow never reached opts.NetworkAllow.
func TestResolveProfileConfig_NetworkAllowMergesWhenModeAlreadySet(t *testing.T) {
	d := newTestDeps(t)
	writeProfile(t, d.Layout, "netmerge", `
network:
  allow:
    - profile.example.com
`)

	ycfg := &config.YoloaiConfig{}
	gcfg := &config.GlobalConfig{}
	// Simulate what the CLI does for --network-allow cli.example.com: promote
	// the mode to isolated and seed opts.NetworkAllow, before the create
	// pipeline (and this function) ever runs.
	opts := &Options{
		Name:         "sb-netmerge",
		Profile:      "netmerge",
		Agent:        "claude",
		Network:      NetworkModeIsolated,
		NetworkAllow: []string{"cli.example.com"},
	}
	agentDef := agent.GetAgent("claude")

	_, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
	require.NoError(t, err)

	// Mode is unchanged (already isolated; promotion is a no-op here either way).
	assert.Equal(t, NetworkModeIsolated, opts.Network)
	// Both the CLI-supplied and the profile's allowlist entries survive.
	assert.Contains(t, opts.NetworkAllow, "cli.example.com")
	assert.Contains(t, opts.NetworkAllow, "profile.example.com")
}

// TestResolveProfileConfig_NoProfileSeedsFromUserConfig is the regression
// guard for the unaffected no-profile path: without --profile, resolveProfileConfig
// must still seed straight from the user's defaults/config.yaml, exactly as
// before DF207's fix (which only changes the profile-active branch).
func TestResolveProfileConfig_NoProfileSeedsFromUserConfig(t *testing.T) {
	d := newTestDeps(t)
	ycfg := &config.YoloaiConfig{
		Env:                map[string]string{"PERSONAL_VAR": "personal-value"},
		AgentArgs:          map[string]string{"claude": "--personal-flag"},
		AutoCommitInterval: 42,
	}
	gcfg := &config.GlobalConfig{}
	opts := &Options{Name: "sb3", Agent: "claude"} // Profile == ""
	agentDef := agent.GetAgent("claude")

	pr, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
	require.NoError(t, err)

	assert.Equal(t, "personal-value", pr.env["PERSONAL_VAR"])
	assert.Equal(t, "--personal-flag", pr.agentArgs["claude"])
	assert.Equal(t, 42, pr.autoCommitInterval)
	assert.Equal(t, config.BaseImage, pr.imageRef)
}

// TestResolveProfileConfig_AgentPrecedence confirms the precedence order is
// unaffected by the DF207 fix: CLI --agent beats profile agent:, and profile
// agent: beats a personal agent: default. baseAgent (ycfg.Agent) is a
// different, legitimate use of the personal config — detecting whether the
// CLI overrode the agent — and DF207 does not touch it.
func TestResolveProfileConfig_AgentPrecedence(t *testing.T) {
	// The "default install" case is the one that mattered and the one the
	// previous version of this test could not reach (DF213). It hand-built
	// ycfg = {Agent: "claude"}, i.e. a user who had edited defaults/config.yaml
	// to set an agent explicitly — so it exercised the only shape where the
	// old comparison happened to work, and certified a path that was broken for
	// everyone else. A fresh install's defaults/config.yaml has every line
	// commented out, so ycfg.Agent is "" while the CLI has already resolved
	// opts.Agent to "claude" via LoadDefaultsConfig.
	t.Run("profile agent wins on a default install, where the user config sets nothing", func(t *testing.T) {
		d := newTestDeps(t)
		writeProfile(t, d.Layout, "agentprofile", "agent: aider\n")

		ycfg := &config.YoloaiConfig{} // fresh install: nothing set
		gcfg := &config.GlobalConfig{}
		opts := &Options{Name: "sb4", Profile: "agentprofile", Agent: "claude"}
		agentDef := agent.GetAgent("claude")

		_, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
		require.NoError(t, err)

		assert.Equal(t, "aider", opts.Agent, "a profile's agent: must apply when the user passed no --agent")
	})

	t.Run("profile agent wins over an agent set in the user config", func(t *testing.T) {
		d := newTestDeps(t)
		writeProfile(t, d.Layout, "agentprofile2", "agent: aider\n")

		ycfg := &config.YoloaiConfig{Agent: "gemini"}
		gcfg := &config.GlobalConfig{}
		// The CLI resolved opts.Agent from that same personal default.
		opts := &Options{Name: "sb5", Profile: "agentprofile2", Agent: "gemini"}
		agentDef := agent.GetAgent("gemini")

		_, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
		require.NoError(t, err)

		assert.Equal(t, "aider", opts.Agent)
	})

	t.Run("CLI --agent wins over profile agent", func(t *testing.T) {
		d := newTestDeps(t)
		writeProfile(t, d.Layout, "agentprofile3", "agent: aider\n")

		ycfg := &config.YoloaiConfig{}
		gcfg := &config.GlobalConfig{}
		// An explicit --agent codex: opts.Agent differs from the resolved default.
		opts := &Options{Name: "sb6", Profile: "agentprofile3", Agent: "codex"}
		agentDef := agent.GetAgent("codex")

		_, err := resolveProfileConfig(context.Background(), d, opts, &agentDef, ycfg, gcfg)
		require.NoError(t, err)

		assert.Equal(t, "codex", opts.Agent)
	})
}
