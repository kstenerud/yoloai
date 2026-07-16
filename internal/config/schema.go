// ABOUTME: Deterministic on-disk schema versioning for a realm's DataDir.
// ABOUTME: A plain-int .schema-version stamp drives status + migration; no artifact-sniffing.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// LibrarySchemaVersion is the current on-disk schema version the library
// expects in its DataDir — the target RealmStatus gates on and CreateFreshLibrary
// stamps. Bump this when the library's on-disk layout or file formats change in a
// way that needs a migration step, and document the change in BREAKING-CHANGES.
//
// The chain splits at libraryFrozenVersion: MigrateLibrary + MigrateAgentConfigs
// own the sealed v0->v3 ladder; anything past it (v3->v4 overlay flatten, v4->v5
// principal rename) is a crash-safe framework migration that owns its own stamp
// write, flipped LAST — after its per-sandbox pass is durable — so the stamp is
// never ahead of the data (the truth invariant). MigrateLibrary must NOT advance
// the stamp past libraryFrozenVersion, or it would green the gate over
// un-migrated sandboxes.
const LibrarySchemaVersion = SchemaPrincipalRenamed

// libraryFrozenVersion is the ceiling of the sealed MigrateLibrary +
// MigrateAgentConfigs ladder (the v2->v3 agent.json split and earlier). Framework
// migrations take the realm from here to LibrarySchemaVersion.
const libraryFrozenVersion = 3

// Framework-migration target versions past libraryFrozenVersion. Each framework
// migrator advances the realm one step and stamps its OWN target LAST — after
// its per-sandbox pass is durable — guarded so a re-run never lowers a higher
// stamp. They run in ascending order; the last equals LibrarySchemaVersion.
const (
	// SchemaOverlayFlattened is the target of the v3->v4 overlay flatten (shipped
	// in v0.6.0).
	SchemaOverlayFlattened = 4
	// SchemaPrincipalRenamed is the target of the v4->v5 CLI principal rename:
	// the CLI adopts the "cli" principal and existing "yoloai-<name>" instances
	// are renamed/recreated to "yoloai-cli-<name>" (D126).
	SchemaPrincipalRenamed = 5
)

// LaunchPrefixResolver maps a sandbox's stored backend type (the "backend"
// string in environment.json) to that backend's constant agent-launch wrap
// prefix. It is injected from a layer with runtime access (System.MigrateDataDir)
// so this package stays free of a runtime import. The resolver must
// be a pure lookup over static backend descriptors — it must not construct a
// Runtime or probe for a backend binary, so a Linux host can migrate the
// Tart/Seatbelt sandboxes it cannot itself run. An unknown backend resolves to
// "" (a no-op prepend).
type LaunchPrefixResolver func(backendType string) string

// LayoutStatus is the verdict of a realm status check: what a realm's DataDir
// needs before it can be used. A too-new on-disk version is reported as an
// error from RealmStatus, not as a status value, because it is unrecoverable
// without upgrading the binary.
type LayoutStatus int

const (
	// LayoutFresh means the realm's DataDir is absent or empty: no data to
	// migrate, so it should be created fresh at the current version.
	LayoutFresh LayoutStatus = iota
	// LayoutMigrate means the DataDir exists at an older version and must be
	// brought current before use.
	LayoutMigrate
	// LayoutOK means the DataDir exists at exactly the current version.
	LayoutOK
)

// RealmStatus reports what dataDir needs relative to currentVersion. It is a
// pure, cheap check that looks only at dataDir and its plain-int
// .schema-version stamp — never at sibling realms, config files, or any other
// artifact. Recognizing what unstamped-but-non-empty content actually is
// belongs to the migrate command, not here.
//
//   - dataDir absent or empty      -> LayoutFresh
//   - stamp version  < current     -> LayoutMigrate
//   - stamp version == current     -> LayoutOK
//   - stamp version  > current     -> error (older binary; upgrade yoloai)
//
// A non-empty dataDir with no stamp reads as version 0, so it routes to
// LayoutMigrate (or LayoutOK only if current is 0).
func RealmStatus(dataDir string, currentVersion int) (LayoutStatus, error) {
	info, err := os.Stat(dataDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return LayoutFresh, nil
		}
		return 0, fmt.Errorf("inspect data dir: %w", err)
	}
	if !info.IsDir() {
		// Garbage at the realm path (a plain file): present, not fresh.
		// Route to migrate, which validates and rejects unrecognized
		// content rather than treating it as empty.
		return LayoutMigrate, nil
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return 0, fmt.Errorf("inspect data dir: %w", err)
	}
	if len(entries) == 0 {
		return LayoutFresh, nil
	}
	version, _, err := ReadSchemaVersion(SchemaVersionPathFor(dataDir))
	if err != nil {
		return 0, err
	}
	switch {
	case version > currentVersion:
		return 0, fmt.Errorf("data dir schema version %d is newer than this build supports (%d); upgrade yoloai", version, currentVersion)
	case version < currentVersion:
		return LayoutMigrate, nil
	default:
		return LayoutOK, nil
	}
}

// SchemaVersionPathFor returns dataDir/.schema-version — the stamp path for an
// arbitrary realm DataDir. Layout.SchemaVersionPath() is the library realm's
// convenience wrapper; this lets the shared RealmStatus check serve any realm.
func SchemaVersionPathFor(dataDir string) string {
	return filepath.Join(dataDir, ".schema-version")
}

// ReadSchemaVersion reads the plain-int schema stamp at path. The bool reports
// whether a stamp exists; a missing stamp returns (0, false, nil). The file is
// a bare integer (optionally surrounded by whitespace) — no JSON or YAML.
func ReadSchemaVersion(path string) (version int, exists bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is DataDir/.schema-version
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false, fmt.Errorf("parse schema version %q: %w", string(data), err)
	}
	return v, true, nil
}

// WriteSchemaVersion writes version as a plain integer to the stamp at path.
func WriteSchemaVersion(path string, version int) error {
	if err := fileutil.WriteFile(path, []byte(strconv.Itoa(version)), 0600); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}

// CreateFreshLibrary initializes the library realm's DataDir at the current
// schema version: it creates the directory and writes the version stamp. It is
// deliberately minimal — operational scaffolding (sandboxes/, profiles/,
// default config.yaml, …) stays in the engine's EnsureSetup. The startup gate
// calls this on a genuinely fresh install; direct embedders that own a clean
// dedicated DataDir call it themselves.
func CreateFreshLibrary(layout Layout) error {
	if err := fileutil.MkdirAll(layout.DataDir, 0750); err != nil {
		return fmt.Errorf("create library data dir: %w", err)
	}
	return WriteSchemaVersion(layout.SchemaVersionPath(), LibrarySchemaVersion)
}

// MigrateLibrary brings the library's DataDir up to LibrarySchemaVersion,
// then stamps it. The stamp is the only signal consulted: an unstamped
// DataDir is treated as version 0, migrations run from there, and the new
// version is recorded so every future run dispatches deterministically off
// the stamp rather than re-inspecting on-disk artifacts.
//
// v0 -> v1 is intentionally a no-op transform: the per-sandbox file shapes
// were already current before this stamp existed (the meta.json ->
// environment.json family of renames predates it), so reaching v1 only
// means recording the stamp. The flat -> namespaced directory move is a
// CLI-side concern handled above the library, since it restructures the dir
// above the library's root. v1 -> v2 backfills agent_launch_prefix (W1b).
// v2 -> v3 is a no-op stamp here: the matching per-sandbox work (relocating
// agent/model into agent.json, Q104) runs in System.MigrateDataDir after this,
// in a layer that can import store/agentcfg.
//
// prefixFor resolves a sandbox's stored backend type to its constant launch
// prefix; it is injected so this package stays free of a runtime
// import (see LaunchPrefixResolver).
//
// This is invoked only by the explicit migrate command (and direct embedders
// that own their DataDir) — the engine no longer runs it automatically.
func MigrateLibrary(layout Layout, prefixFor LaunchPrefixResolver) error {
	stampPath := layout.SchemaVersionPath()
	current, _, err := ReadSchemaVersion(stampPath)
	if err != nil {
		return err
	}
	if current > LibrarySchemaVersion {
		return fmt.Errorf("library data dir schema version %d is newer than this build supports (%d); upgrade yoloai", current, LibrarySchemaVersion)
	}
	// The sealed ladder runs only up to libraryFrozenVersion; the framework
	// (v3->v4 overlay flatten) takes it the rest of the way and stamps last. If
	// the realm is already at or past the frozen ceiling, there is nothing here to
	// do and — critically — nothing to stamp: advancing the stamp toward
	// LibrarySchemaVersion here would green the gate over data the framework has
	// not migrated yet.
	if current >= libraryFrozenVersion {
		return nil
	}
	for v := current; v < libraryFrozenVersion; v++ {
		if err := migrateLibraryStep(layout, v, prefixFor); err != nil {
			return fmt.Errorf("library migration v%d -> v%d: %w", v, v+1, err)
		}
	}
	return WriteSchemaVersion(stampPath, libraryFrozenVersion)
}

// migrateLibraryStep applies the single transform that takes the library's
// DataDir from version `from` to `from+1`. Add a case per future bump.
func migrateLibraryStep(layout Layout, from int, prefixFor LaunchPrefixResolver) error {
	switch from {
	case 0:
		// v0 -> v1: no on-disk transform; stamping happens in MigrateLibrary.
		return nil
	case 1:
		// v1 -> v2: backfill agent_launch_prefix into every sandbox.
		return backfillLaunchPrefix(layout, prefixFor)
	case 2:
		// v2 -> v3: no realm-level transform here. The per-sandbox relocation of
		// agent/model out of environment.json into agent.json (Q104) needs the
		// store + agentcfg types, which this package cannot import (store -> config
		// already). That pass runs above the library, in System.MigrateDataDir,
		// after MigrateLibrary stamps the realm. Bumping the realm version is what
		// makes the startup gate force `yoloai system migrate` before any
		// per-sandbox load balks on the slimmed record.
		return nil
	default:
		return fmt.Errorf("no migration registered from version %d", from)
	}
}

// backfillLaunchPrefix (v1 -> v2) writes each sandbox's backend-constant agent
// launch prefix into its runtime-config.json, making agent_launch_prefix the
// single source of truth for the launch wrap (W1b) and retiring the Python
// prepare_launch_command fallback. The prefix is a deterministic per-backend
// constant, so the write is unconditional and idempotent: rewriting an already
// correct value is a no-op, and sandboxes that never had the field get it
// filled. Container backends resolve to "" (a harmless no-op prepend); in
// practice only Tart/Seatbelt sandboxes receive a non-empty string.
func backfillLaunchPrefix(layout Layout, prefixFor LaunchPrefixResolver) error {
	entries, err := os.ReadDir(layout.SandboxesDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // no sandboxes yet
		}
		return fmt.Errorf("read sandboxes dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sandboxDir := filepath.Join(layout.SandboxesDir(), e.Name())
		if err := backfillSandboxLaunchPrefix(sandboxDir, prefixFor); err != nil {
			return fmt.Errorf("sandbox %q: %w", e.Name(), err)
		}
	}
	return nil
}

// backfillSandboxLaunchPrefix backfills one sandbox's agent_launch_prefix. It
// reads the backend type from environment.json, resolves the constant prefix,
// and rewrites runtime-config.json with that value. A sandbox missing either
// file is skipped (a partial/dormant directory with nothing to wrap). The
// rewrite preserves every other field's exact bytes; only the one key changes.
func backfillSandboxLaunchPrefix(sandboxDir string, prefixFor LaunchPrefixResolver) error {
	backend, ok, err := readSandboxBackend(sandboxDir)
	if err != nil {
		return err
	}
	if !ok {
		return nil // no environment.json: can't classify; leave untouched
	}

	rcPath := filepath.Join(sandboxDir, "runtime-config.json")
	rcData, err := os.ReadFile(rcPath) //nolint:gosec // G304: trusted sandbox subpath
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // no runtime-config.json: nothing to wrap
		}
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var rc map[string]json.RawMessage
	if err := json.Unmarshal(rcData, &rc); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}
	prefix, err := json.Marshal(prefixFor(backend))
	if err != nil {
		return fmt.Errorf("encode launch prefix: %w", err)
	}
	rc["agent_launch_prefix"] = prefix

	out, err := json.MarshalIndent(rc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime-config.json: %w", err)
	}
	// runtime-config.json is bind-mounted into the container (read-only, no
	// secrets); 0644 matches the create path's perm.
	if err := fileutil.WriteFilePerm(rcPath, out, 0644); err != nil {
		return fmt.Errorf("write runtime-config.json: %w", err)
	}
	return nil
}

// readSandboxBackend reads the "backend" field from a sandbox's
// environment.json. The bool reports whether the file exists; a missing file
// returns ("", false, nil) so the caller can skip an unclassifiable directory.
func readSandboxBackend(sandboxDir string) (backend string, exists bool, err error) {
	data, err := os.ReadFile(filepath.Join(sandboxDir, "environment.json")) //nolint:gosec // G304: trusted sandbox subpath
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read environment.json: %w", err)
	}
	var env struct {
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", false, fmt.Errorf("parse environment.json: %w", err)
	}
	return env.Backend, true, nil
}
