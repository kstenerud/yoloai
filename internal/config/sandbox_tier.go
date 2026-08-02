// ABOUTME: Classifies every sandbox-root entry into its guest-access tier — the
// ABOUTME: one statement of the v6 layout, used by the builders and the migrator.
package config

import "path/filepath"

// Tier is a sandbox entry's guest-access class, and the directory it lives in.
// The two are deliberately the same thing: the tier is a physical directory, so
// classification is "which directory does this file sit in" rather than a list
// somewhere that can drift out of step with the files.
type Tier string

const (
	// TierHost is never shared into any guest, on any backend.
	TierHost Tier = HostTierName
	// TierReadOnly is readable by the guest and must not be writable by it.
	TierReadOnly Tier = ReadOnlyTierName
	// TierReadWrite is the guest's read-write region.
	TierReadWrite Tier = ReadWriteTierName
)

// TierDir returns tier's directory within a sandbox directory.
func TierDir(sandboxDir string, tier Tier) string {
	return filepath.Join(sandboxDir, string(tier))
}

// entryTiers maps each known sandbox-root entry name to its tier.
//
// This table is the v5->v6 migration's specification: it is what the mover
// consults to decide where every entry of an existing flat sandbox goes, so a
// wrong row here relocates real user data into the wrong access class. Rows are
// grouped by tier and every one of them is a decision recorded in the plan
// (design/plans/sandbox-share-tiering.md), not an inference from the file's
// name.
//
// Four entries have no path builder and so appear only here: the guest's HOME,
// tart's setup log, and the two xcodebuild first-launch artifacts written by
// runtime/monitor/sandbox-setup.py. They are the reason this is keyed on entry
// names rather than derived from the builders — a builder-derived table can
// only ever know about files the host writes through a builder, and those four
// are exactly the class it would miss.
var entryTiers = map[string]Tier{
	// Host-only: sandbox bookkeeping the guest must never reach (DF136).
	EnvironmentFileName:    TierHost,
	SandboxStateFileName:   TierHost,
	AgentConfigFileName:    TierHost,
	NetpolicyFileName:      TierHost,
	NetworkDiagFileName:    TierHost,
	BackendDirName:         TierHost,
	InjectorRecordFileName: TierHost,
	InjectorLogFileName:    TierHost,
	InjectorTokenFileName:  TierHost,
	ContextFileName:        TierHost,

	// Guest-readable, guest-must-not-write.
	RuntimeConfigFileName: TierReadOnly,
	BinDirName:            TierReadOnly,
	PromptFileName:        TierReadOnly,
	ResumePromptFileName:  TierReadOnly,
	MachineIDFileName:     TierReadOnly,
	HomeSeedDirName:       TierReadOnly,
	SecretsDirName:        TierReadOnly,

	// Guest read-write.
	LogsDirName:                      TierReadWrite,
	AgentRuntimeDirName:              TierReadWrite,
	AgentStatusFileName:              TierReadWrite,
	FilesDirName:                     TierReadWrite,
	CacheDirName:                     TierReadWrite,
	WorkDirName:                      TierReadWrite,
	TmuxDirName:                      TierReadWrite,
	VSCodeCLIDirName:                 TierReadWrite,
	CreateDoneMarkerName:             TierReadWrite,
	ContainerLogFileName:             TierReadWrite,
	"home":                           TierReadWrite,
	"setup.log":                      TierReadWrite,
	"xcodebuild-firstlaunch.log":     TierReadWrite,
	"xcodebuild-firstlaunch.started": TierReadWrite,
}

// TierOfEntry classifies a sandbox-root entry by name. It is total: an entry
// nobody has classified still gets a tier, and recognized is false so the caller
// can say so out loud.
//
// The unknown default is TierHost, and the direction matters more than the
// choice. Host-only is the *fail-safe* answer: misfiling something into host/
// can only take guest access away, which shows up as a missing file at runtime
// and gets reported. Defaulting to read-write would silently hand the agent
// something nobody classified, and nothing would ever surface it.
//
// The tier directory names themselves are not entries and never classify —
// a sandbox root holding one is already tiered (or half-tiered), which is a
// different thing from an unclassified entry and is the migrator's to refuse.
func TierOfEntry(name string) (tier Tier, recognized bool) {
	if IsTierName(name) {
		return TierHost, false
	}
	if t, ok := entryTiers[name]; ok {
		return t, true
	}
	return TierHost, false
}

// IsTierName reports whether name is one of the three tier directories.
func IsTierName(name string) bool {
	switch name {
	case HostTierName, ReadOnlyTierName, ReadWriteTierName:
		return true
	}
	return false
}
