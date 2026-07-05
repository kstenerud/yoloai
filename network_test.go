// ABOUTME: Tests for Sandbox.Network() sub-handle. Filesystem-backed reads;
// ABOUTME: writes/live-patching tested via newTestClient's tempdir layout.

package yoloai

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/netpolicycfg"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/store"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeIsolatedSandbox creates a fake :isolated sandbox dir with a
// environment.json carrying the given allowlist. Avoids spinning up an
// actual sandbox + runtime, which Network read/derivation tests
// don't need.
//
// Also writes a minimal runtime-config.json so the Allow/Deny
// PatchConfigAllowedDomains call has a target file to patch.
func writeIsolatedSandbox(t *testing.T, c *System, name, agentName string, allow []string) {
	t.Helper()
	sandboxDir := c.layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: agentName}))
	// Network policy now lives in netpolicy.json (D90), not the substrate record.
	require.NoError(t, netpolicycfg.Save(sandboxDir, &netpolicycfg.Netpolicy{Mode: "isolated", Allow: allow}))

	// PatchConfigAllowedDomains reads + writes runtime-config.json.
	// A minimal `{}` is enough — the patch helper inserts the
	// allowed_domains field.
	require.NoError(t, os.WriteFile(
		filepath.Join(sandboxDir, store.RuntimeConfigFile),
		[]byte("{}\n"), 0600,
	))
}

// writeNoNetworkSandbox creates a sandbox dir with NetworkMode:"none"
// — used to assert UsageError handling.
func writeNoNetworkSandbox(t *testing.T, c *System, name string) {
	t.Helper()
	sandboxDir := c.layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "test"}))
	// Network policy now lives in netpolicy.json (D90), not the substrate record.
	require.NoError(t, netpolicycfg.Save(sandboxDir, &netpolicycfg.Netpolicy{Mode: "none"}))
}

// clientWithSandbox returns a yoloai.Client wired up against a
// tempdir layout. System and Client share the layout so test
// fixtures written via System appear to Client reads.
func clientWithSandbox(t *testing.T) (*Client, *System) {
	t.Helper()
	sys := newTestClient(t)
	// Backend-less Engine sharing the System's layout — Network.Allowed and
	// agent-set derivation don't touch the runtime, and the live-patch path
	// soft-fails (Runtime() is nil) rather than panicking.
	c := &Client{
		layout: sys.layout,
		engine: orchestrator.NewEngine("", slog.Default(), bytes.NewReader(nil), orchestrator.WithLayout(sys.layout)),
	}
	return c, sys
}

// mustSandbox returns a validated sandbox handle, failing the test if the
// sandbox doesn't exist (Client.Sandbox now validates at construction — F22).
func mustSandbox(t *testing.T, c *Client, name string) *Sandbox {
	t.Helper()
	sb, err := c.Sandbox(name)
	require.NoError(t, err)
	return sb
}

// --- AllowedDomain derivation ---

func TestNetwork_Allowed_NoIsolation_Empty(t *testing.T) {
	c, sys := clientWithSandbox(t)

	// Sandbox with no network mode at all.
	sandboxDir := sys.layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{
		Name:      "box",
		CreatedAt: time.Now(),
	}))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))

	allowed, err := mustSandbox(t, c, "box").Network().Allowed(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, allowed)
	assert.Empty(t, allowed)
}

// TestNetwork_Mode covers the daemon-free configured-mode read: Mode() branches
// the `allowed` command without a backend, so listing works on a stopped/
// unreachable sandbox (the mode is netpolicy.json intent — D90).
func TestNetwork_Mode(t *testing.T) {
	c, sys := clientWithSandbox(t)

	writeIsolatedSandbox(t, sys, "iso", "claude", []string{"example.com"})
	mode, err := mustSandbox(t, c, "iso").Network().Mode()
	require.NoError(t, err)
	assert.Equal(t, NetworkModeIsolated, mode)

	writeNoNetworkSandbox(t, sys, "nonet")
	mode, err = mustSandbox(t, c, "nonet").Network().Mode()
	require.NoError(t, err)
	assert.Equal(t, NetworkModeNone, mode)

	// No netpolicy.json at all (default networking): Load returns a zero-value
	// record, so the mode reads back as "" (NetworkModeDefault).
	sandboxDir := sys.layout.SandboxDir("open")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{Name: "open", CreatedAt: time.Now()}))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))
	mode, err = mustSandbox(t, c, "open").Network().Mode()
	require.NoError(t, err)
	assert.Equal(t, NetworkModeDefault, mode)
}

func TestNetwork_Allowed_AgentRequirement_Provenance(t *testing.T) {
	c, sys := clientWithSandbox(t)
	// Claude's NetworkAllowlist includes api.anthropic.com.
	writeIsolatedSandbox(t, sys, "box", "claude", []string{
		"api.anthropic.com",
		"example.com",
	})

	allowed, err := mustSandbox(t, c, "box").Network().Allowed(context.Background())
	require.NoError(t, err)
	require.Len(t, allowed, 2)

	bySource := map[string]DomainSource{}
	for _, d := range allowed {
		bySource[d.Domain] = d.Source
	}
	assert.Equal(t, AllowedFromAgentRequirement, bySource["api.anthropic.com"],
		"claude's baked-in domain must come back tagged as agent-requirement")
	assert.Equal(t, AllowedFromUser, bySource["example.com"],
		"a domain absent from the agent definition must come back as user-added")
}

func TestNetwork_Allowed_UnknownAgent_AllUser(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "ghost-agent", []string{"a.example", "b.example"})

	allowed, err := mustSandbox(t, c, "box").Network().Allowed(context.Background())
	require.NoError(t, err)
	require.Len(t, allowed, 2)
	for _, d := range allowed {
		assert.Equal(t, AllowedFromUser, d.Source,
			"unknown agent → no derivable requirements → everything looks user-added")
	}
}

func TestNetwork_Allowed_PreservesOrder(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", []string{
		"example.com",
		"api.anthropic.com",
		"other.example",
	})

	allowed, err := mustSandbox(t, c, "box").Network().Allowed(context.Background())
	require.NoError(t, err)
	require.Len(t, allowed, 3)
	assert.Equal(t, "example.com", allowed[0].Domain, "Allowed() preserves meta-on-disk order")
	assert.Equal(t, "api.anthropic.com", allowed[1].Domain)
	assert.Equal(t, "other.example", allowed[2].Domain)
}

func TestNetwork_Allowed_NotFound(t *testing.T) {
	c, _ := clientWithSandbox(t)
	// F22: a missing sandbox is rejected at handle construction, where the name
	// was typed — not lazily inside Network().Allowed.
	_, err := c.Sandbox("ghost")
	assert.ErrorIs(t, err, orchestrator.ErrSandboxNotFound)
}

// --- Allow ---

func TestNetwork_Allow_AddsNewDomains(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", []string{"api.anthropic.com"})

	result, err := mustSandbox(t, c, "box").Network().Allow(context.Background(), "extra.example", "other.example")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.ElementsMatch(t, []string{"extra.example", "other.example"}, result.Added)
	// Sandbox isn't running → live-patch soft-fails; persistence
	// still happened.
	assert.False(t, result.Live)

	// Re-read via Allowed() to confirm persistence and provenance.
	allowed, err := mustSandbox(t, c, "box").Network().Allowed(context.Background())
	require.NoError(t, err)
	require.Len(t, allowed, 3)
	domains := map[string]DomainSource{}
	for _, d := range allowed {
		domains[d.Domain] = d.Source
	}
	assert.Equal(t, AllowedFromAgentRequirement, domains["api.anthropic.com"])
	assert.Equal(t, AllowedFromUser, domains["extra.example"])
	assert.Equal(t, AllowedFromUser, domains["other.example"])
}

func TestNetwork_Allow_DedupsAgainstExisting(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", []string{"a.example", "b.example"})

	result, err := mustSandbox(t, c, "box").Network().Allow(context.Background(), "a.example", "c.example", "b.example")
	require.NoError(t, err)
	assert.Equal(t, []string{"c.example"}, result.Added,
		"Allow must drop entries that already exist; only the genuinely new domain is reported")
}

func TestNetwork_Allow_DedupsWithinInput(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", nil)

	result, err := mustSandbox(t, c, "box").Network().Allow(context.Background(), "x.example", "x.example", "y.example")
	require.NoError(t, err)
	assert.Equal(t, []string{"x.example", "y.example"}, result.Added,
		"Allow must dedupe within the input slice too — duplicates cause one add")
}

func TestNetwork_Allow_AllExisting_NoAdds(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", []string{"a.example"})

	result, err := mustSandbox(t, c, "box").Network().Allow(context.Background(), "a.example")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotNil(t, result.Added, "Added is non-nil empty slice (JSON renders [])")
	assert.Empty(t, result.Added)
	assert.False(t, result.Live)
}

func TestNetwork_Allow_NoDomains_UsageError(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", nil)

	_, err := mustSandbox(t, c, "box").Network().Allow(context.Background())
	require.Error(t, err)
	var usage *yoerrors.UsageError
	assert.ErrorAs(t, err, &usage)
}

func TestNetwork_Allow_NoneNetworkMode_UsageError(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeNoNetworkSandbox(t, sys, "box")

	_, err := mustSandbox(t, c, "box").Network().Allow(context.Background(), "a.example")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "--network-none")
}

func TestNetwork_Allow_NotIsolated_UsageError(t *testing.T) {
	c, sys := clientWithSandbox(t)

	sandboxDir := sys.layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, store.SaveEnvironment(sandboxDir, &store.Environment{
		Name:      "box",
		CreatedAt: time.Now(),
		// no NetworkMode set
	}))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))

	_, err := mustSandbox(t, c, "box").Network().Allow(context.Background(), "a.example")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "not using network isolation")
}

// --- Deny ---

func TestNetwork_Deny_RemovesAndTagsSource(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", []string{
		"api.anthropic.com", // agent-required
		"example.com",       // user
	})

	result, err := mustSandbox(t, c, "box").Network().Deny(context.Background(), "api.anthropic.com", "example.com")
	require.NoError(t, err)
	require.Len(t, result.Removed, 2)

	bySource := map[string]DomainSource{}
	for _, d := range result.Removed {
		bySource[d.Domain] = d.Source
	}
	assert.Equal(t, AllowedFromAgentRequirement, bySource["api.anthropic.com"],
		"Deny must surface that an agent-required domain was removed so UIs can warn")
	assert.Equal(t, AllowedFromUser, bySource["example.com"])

	// Allowlist is empty now on disk (netpolicy.json, not environment.json — D90).
	np, err := netpolicycfg.Load(sys.layout.SandboxDir("box"))
	require.NoError(t, err)
	assert.Empty(t, np.Allow)
}

func TestNetwork_Deny_DomainNotInList_UsageError(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", []string{"a.example"})

	_, err := mustSandbox(t, c, "box").Network().Deny(context.Background(), "absent.example")
	require.Error(t, err)
	var usage *yoerrors.UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "not in the allowlist")
}

func TestNetwork_Deny_PartialFailureRollsBack(t *testing.T) {
	c, sys := clientWithSandbox(t)
	// Validate-before-remove: if ANY domain is missing, NOTHING is
	// removed. Locks in the existing UX guarantee against typo
	// half-commits.
	writeIsolatedSandbox(t, sys, "box", "claude", []string{"a.example", "b.example"})

	_, err := mustSandbox(t, c, "box").Network().Deny(context.Background(), "a.example", "absent.example")
	require.Error(t, err)

	np, err := netpolicycfg.Load(sys.layout.SandboxDir("box"))
	require.NoError(t, err)
	assert.Equal(t, []string{"a.example", "b.example"}, np.Allow,
		"validation failure must leave the allowlist untouched")
}

func TestNetwork_Deny_NoDomains_UsageError(t *testing.T) {
	c, sys := clientWithSandbox(t)
	writeIsolatedSandbox(t, sys, "box", "claude", []string{"a.example"})

	_, err := mustSandbox(t, c, "box").Network().Deny(context.Background())
	require.Error(t, err)
	var usage *yoerrors.UsageError
	assert.ErrorAs(t, err, &usage)
}

// --- pure helper: computeAllowedDomains ---
// (Direct unit coverage so future refactors of the storage shape
// can't break provenance computation without a loud test.)

func TestComputeAllowedDomains_ClaudeAgent(t *testing.T) {
	// computeAllowedDomains takes the allow slice directly (D90: np.Allow).
	out := computeAllowedDomains([]string{"api.anthropic.com", "extra.example"}, "claude")
	require.Len(t, out, 2)
	assert.Equal(t, "api.anthropic.com", out[0].Domain)
	assert.Equal(t, AllowedFromAgentRequirement, out[0].Source)
	assert.Equal(t, "extra.example", out[1].Domain)
	assert.Equal(t, AllowedFromUser, out[1].Source)
}

func TestComputeAllowedDomains_EmptyAllow(t *testing.T) {
	out := computeAllowedDomains(nil, "claude")
	assert.NotNil(t, out)
	assert.Empty(t, out)
}

// Smoke-guard the layout helper used by the suite.
func TestNetworkTest_Layout(t *testing.T) {
	c, _ := clientWithSandbox(t)
	assert.NotEmpty(t, c.layout.SandboxesDir())
	// Walk up: parent must exist.
	require.DirExists(t, filepath.Dir(c.layout.SandboxesDir()))
}
