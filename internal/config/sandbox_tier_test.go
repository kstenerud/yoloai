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

// TestTierOfEntry_AgreesWithTheTieredBuilders is the drift guard between the two
// representations of one fact. The path builders say where a file goes; the
// entry table says where the v5->v6 mover puts it. If they disagree, migration
// files a record somewhere the running binary does not look for it — which is
// invisible to both of them, since each is self-consistent.
//
// All three tiers are covered. Every per-sandbox builder that resolves into a
// tier appears below; a builder that moves without its table row, or a row that
// moves without its builder, fails here and nowhere else.
func TestTierOfEntry_AgreesWithTheTieredBuilders(t *testing.T) {
	const sb = "/sandboxes/box"
	hostBuilders := map[string]string{
		EnvironmentFileName:    EnvironmentPath(sb),
		SandboxStateFileName:   SandboxStatePath(sb),
		AgentConfigFileName:    AgentConfigPath(sb),
		NetpolicyFileName:      NetpolicyPath(sb),
		NetworkDiagFileName:    NetworkDiagPath(sb),
		BackendDirName:         BackendPath(sb),
		InjectorRecordFileName: InjectorRecordPath(sb),
		InjectorLogFileName:    InjectorLogPath(sb),
		InjectorTokenFileName:  InjectorTokenPath(sb),
		ContextFileName:        ContextPath(sb),
	}
	readOnly := map[string]string{
		RuntimeConfigFileName: RuntimeConfigPath(sb),
		BinDirName:            BinPath(sb),
		PromptFileName:        PromptPath(sb),
		ResumePromptFileName:  ResumePromptPath(sb),
		MachineIDFileName:     MachineIDPath(sb),
		HomeSeedDirName:       HomeSeedPath(sb),
		SecretsDirName:        SecretsPath(sb),
	}
	readWrite := map[string]string{
		LogsDirName:          LogsPath(sb),
		AgentStatusFileName:  AgentStatusPath(sb),
		AgentRuntimeDirName:  AgentRuntimePath(sb),
		FilesDirName:         FilesPath(sb),
		CacheDirName:         CachePath(sb),
		WorkDirName:          WorkBasePath(sb),
		TmuxDirName:          TmuxPath(sb),
		VSCodeCLIDirName:     VSCodeCLIPath(sb),
		ContainerLogFileName: ContainerLogPath(sb),
		CreateDoneMarkerName: CreateDoneMarkerPath(sb),
	}
	all := map[string]string{}
	for _, m := range []map[string]string{hostBuilders, readOnly, readWrite} {
		for k, v := range m {
			all[k] = v
		}
	}
	for name, builder := range all {
		tier, recognized := TierOfEntry(name)
		if !recognized {
			t.Errorf("%s has a path builder but no tier — the mover would file it as unclassified", name)
			continue
		}
		if want := filepath.Join(TierDir(sb, tier), name); builder != want {
			t.Errorf("%s: builder says %q, the entry table says %q — migration and runtime disagree about where this lives",
				name, builder, want)
		}
	}
}

// Every entry name the layout knows about must classify. A new constant with no
// row lands in host/ as "unclassified", which for a guest-writable file means it
// silently stops being reachable — reported at migration time, but only if
// somebody reads the plan.
func TestTierOfEntry_ClassifiesEveryNamedEntry(t *testing.T) {
	// Deliberately literal, like the rest of this file: derived from the same
	// table under test, it could only ever agree with itself.
	named := []string{
		EnvironmentFileName, SandboxStateFileName, AgentConfigFileName, NetpolicyFileName,
		NetworkDiagFileName, InjectorRecordFileName, InjectorLogFileName, InjectorTokenFileName,
		ContextFileName, BackendDirName,
		RuntimeConfigFileName, PromptFileName, ResumePromptFileName, MachineIDFileName,
		HomeSeedDirName, SecretsDirName, BinDirName,
		AgentStatusFileName, LogsDirName, FilesDirName, CacheDirName, WorkDirName,
		VSCodeCLIDirName, TmuxDirName, AgentRuntimeDirName, ContainerLogFileName,
		CreateDoneMarkerName,
	}
	for _, name := range named {
		if _, recognized := TierOfEntry(name); !recognized {
			t.Errorf("%q is a named sandbox entry with no tier", name)
		}
	}
}

// The unknown default is host/, and it is a decision rather than a fallback: it
// can only remove guest access, which surfaces as a missing file, whereas a
// read-write default would silently grant the agent something nobody classified.
func TestTierOfEntry_UnknownDefaultsToHostAndSaysSo(t *testing.T) {
	tier, recognized := TierOfEntry("something-nobody-classified")
	if recognized {
		t.Error("an unknown entry must report itself unrecognized")
	}
	if tier != TierHost {
		t.Errorf("unknown entry classified as %q, want %q (the fail-safe direction)", tier, TierHost)
	}
}

// A tier directory is not an entry. A sandbox root holding one is already tiered,
// which the migrator refuses as a distinct case — classifying it as an ordinary
// unknown would nest host/ inside host/.
func TestTierOfEntry_TierNamesAreNotEntries(t *testing.T) {
	for _, name := range []string{HostTierName, ReadOnlyTierName, ReadWriteTierName} {
		if _, recognized := TierOfEntry(name); recognized {
			t.Errorf("%q classified as an ordinary entry; it is a tier", name)
		}
		if !IsTierName(name) {
			t.Errorf("IsTierName(%q) = false", name)
		}
	}
	if IsTierName("host-scratch") {
		t.Error("IsTierName must not match a sibling whose name merely starts with a tier name")
	}
}
