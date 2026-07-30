// ABOUTME: Pins which tier each per-sandbox path resolves into. The one place
// ABOUTME: that asserts the layout literally, so a tier move is red exactly here.
package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSandboxLayout_TierMembershipIsPinned asserts, literally, which tier every
// per-sandbox path builder resolves into.
//
// It exists because every other test in the tree now goes *through* the builders
// — which is correct for them, and means none of them would notice a file
// silently changing tier. A guest-access class that no test states is a class
// that drifts. So the assertions here are deliberately spelled out as string
// prefixes rather than composed from the same helpers under test: composing them
// would make this file agree with any layout, including a wrong one.
//
// The tier a file sits in *is* its guest-access class (see the plan), so a
// failure here is not a naming nit — it means something moved between "the guest
// can never see this", "the guest may read this" and "the guest may write this".
func TestSandboxLayout_TierMembershipIsPinned(t *testing.T) {
	const sb = "/sandboxes/box"

	hostTier := map[string]string{
		"environment.json":   EnvironmentPath(sb),
		"sandbox-state.json": SandboxStatePath(sb),
		"agent.json":         AgentConfigPath(sb),
		"netpolicy.json":     NetpolicyPath(sb),
		"network-diag.txt":   NetworkDiagPath(sb),
		"backend/":           BackendPath(sb),
		"injector.json":      InjectorRecordPath(sb),
		"injector.log":       InjectorLogPath(sb),
		"injector-token":     InjectorTokenPath(sb),
		// Host-side reference copy. The agent's own context file is a different
		// file, written into agent-runtime/ — conflating the two put this in the
		// read-only tier until it was checked against its consumers (2026-07-30).
		"context.md": ContextPath(sb),
	}
	for name, got := range hostTier {
		want := filepath.Join(sb, HostTierName)
		if !strings.HasPrefix(got, want+"/") {
			t.Errorf("%s must be host-only (never shared to any guest): got %q, want under %q", name, got, want)
		}
	}

	// The injector token is the sharpest case: its host-only placement is the
	// only thing stopping a co-resident container from reading another sandbox's
	// token, so it gets its own assertion rather than riding the loop.
	if tok := InjectorTokenPath(sb); !strings.HasPrefix(tok, filepath.Join(sb, HostTierName)+"/") {
		t.Errorf("injector-token left the host tier (%q) — a guest-visible token is cross-sandbox readable", tok)
	}
}

// TestSandboxLayout_HostTierIsNotReachableByPrefixConfusion guards the one way a
// path can be inside host/ by string and outside it in fact: a sibling directory
// whose name merely starts with the tier name (host-scratch/) would satisfy a
// naive prefix check while being an entirely different, shareable directory.
func TestSandboxLayout_HostTierIsNotReachableByPrefixConfusion(t *testing.T) {
	const sb = "/sandboxes/box"
	tier := HostTierDir(sb)

	if got := filepath.Join(sb, HostTierName+"-scratch", "x"); strings.HasPrefix(got, tier+"/") {
		t.Fatalf("prefix check is unsound: %q must not read as inside %q", got, tier)
	}
	if !strings.HasPrefix(EnvironmentPath(sb), tier+"/") {
		t.Fatalf("environment.json must be inside %q", tier)
	}
}
