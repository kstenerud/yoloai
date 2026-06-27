// ABOUTME: Sandbox environment metadata (environment.json) load/save — the
// ABOUTME: substrate record: directories, posture, provenance, creation settings.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
)

// metaVersion is the current schema version for Environment. Bump when adding or
// changing fields that require migration from older sandboxes.
//
// v2 collapsed the singular Workdir field + separate Directories slice into one
// ordered Dirs list (element 0 is the workdir). See migrate().
// v3 removed the inside-process config (agent, model) from the substrate record;
// it now lives in the sibling agent.json (Q104). Because that relocation is
// cross-file, it is NOT a step in the in-struct migrate() ladder — it is an
// explicit per-sandbox pass run by `yoloai system migrate` (see
// orchestrator.MigrateAgentConfigs). LoadEnvironment balks on any record below
// v3 rather than migrating on read (D61: no write-on-read).
const metaVersion = 3

// Environment holds sandbox configuration captured at creation time.
type Environment struct {
	Version       int                     `json:"version"` // schema version; 0 = legacy (pre-versioning)
	YoloaiVersion string                  `json:"yoloai_version"`
	Name          string                  `json:"name"`
	Principal     config.PrincipalSegment `json:"principal,omitempty"` // owning principal; "" = default (no-principal). Attribution + runtime namespace (D62).
	CreatedAt     time.Time               `json:"created_at"`
	BackendType   runtime.BackendType     `json:"backend"` // typed string; serializes as "docker"/"tart"/etc.
	Profile       string                  `json:"profile,omitempty"`
	ImageRef      string                  `json:"image_ref,omitempty"`

	// Headless records whether the agent runs in its own headless mode (prompt
	// baked into the launch command, pane-death = done) versus the interactive
	// TTY flow. This is the EFFECTIVE mode chosen at create: `yoloai run` requests
	// headless, but create downgrades to interactive when the agent's headless
	// mode would be unsafe without an API key (D101). Consumers read it back to
	// pick a completion signal (exit for headless, idle for interactive).
	Headless bool `json:"headless,omitempty"`

	// Dirs is the ordered list of directories the sandbox manages. Element 0 is
	// the workdir (the agent's cwd; "the workdir" for docs/UI). Entries with
	// Mode copy/overlay are "tracked" (diff/apply applies to them); rw/ro are
	// reference mounts. Use the Workdir()/AuxDirs() helpers rather than indexing.
	Dirs []DirEnvironment `json:"dirs,omitempty"`

	// LegacyWorkdir / LegacyDirectories are the pre-v2 schema fields, retained
	// only so old environment.json files unmarshal and migrate() can repack them
	// into Dirs. Never written back: migrate() clears them and omitempty drops
	// them on save. Do not read these outside migrate().
	LegacyWorkdir     *DirEnvironment  `json:"workdir,omitempty"`
	LegacyDirectories []DirEnvironment `json:"directories,omitempty"`

	HasPrompt          bool                   `json:"has_prompt"`
	Ports              []string               `json:"ports,omitempty"`
	Resources          *config.ResourceLimits `json:"resources,omitempty"`
	Mounts             []string               `json:"mounts,omitempty"`
	CapAdd             []string               `json:"cap_add,omitempty"`
	Devices            []string               `json:"devices,omitempty"`
	Setup              []string               `json:"setup,omitempty"`
	AutoCommitInterval int                    `json:"auto_commit_interval,omitempty"`
	Debug              bool                   `json:"debug,omitempty"`
	UsernsMode         string                 `json:"userns_mode,omitempty"`     // "keep-id" for Podman rootless keep-id; "" otherwise
	Isolation          runtime.IsolationMode  `json:"isolation,omitempty"`       // isolation mode: container, container-enhanced, vm, vm-enhanced
	HostFilesystem     bool                   `json:"host_filesystem,omitempty"` // true when sandbox state lives on the host (seatbelt)
	VscodeTunnel       bool                   `json:"vscode_tunnel,omitempty"`   // true when VS Code Remote Tunnel is enabled
	Archetype          string                 `json:"archetype,omitempty"`       // resolved environment archetype (simple, compose, devcontainer, apple)
}

// DirEnvironment stores resolved directory state at creation time, for every
// entry in Environment.Dirs (workdir and auxiliary alike — the workdir is just
// Dirs[0]). InceptionSHA / BaselineSHA are meaningful only for tracked
// (copy/overlay) entries.
type DirEnvironment struct {
	HostPath     string  `json:"host_path"`
	MountPath    string  `json:"mount_path"`
	Mode         DirMode `json:"mode"` // typed; serializes as "copy"/"overlay"/"rw"/"ro"
	BaselineSHA  string  `json:"baseline_sha,omitempty"`
	InceptionSHA string  `json:"inception_sha,omitempty"`
}

// Workdir returns the primary directory — Dirs[0], the agent's cwd. Returns nil
// only for a malformed environment with no directories. The returned pointer
// aliases the slice element, so writes through it (e.g. BaselineSHA updates)
// persist on the next SaveEnvironment.
func (e *Environment) Workdir() *DirEnvironment {
	if len(e.Dirs) == 0 {
		return nil
	}
	return &e.Dirs[0]
}

// AuxDirs returns the non-workdir directories (Dirs[1:]).
func (e *Environment) AuxDirs() []DirEnvironment {
	if len(e.Dirs) <= 1 {
		return nil
	}
	return e.Dirs[1:]
}

// TrackedDirs returns the indices into Dirs of the tracked (copy/overlay)
// entries — those diff/apply operates on. Returns indices (not copies) so
// callers can write back BaselineSHA via &e.Dirs[i].
func (e *Environment) TrackedDirs() []int {
	var idx []int
	for i := range e.Dirs {
		if e.Dirs[i].Mode == DirModeCopy || e.Dirs[i].Mode == DirModeOverlay {
			idx = append(idx, i)
		}
	}
	return idx
}

// Dir returns the directory entry addressed by hostPath, or the workdir
// (Dirs[0]) when hostPath is "". Returns nil if hostPath matches no entry.
func (e *Environment) Dir(hostPath string) *DirEnvironment {
	if hostPath == "" {
		return e.Workdir()
	}
	for i := range e.Dirs {
		if e.Dirs[i].HostPath == hostPath {
			return &e.Dirs[i]
		}
	}
	return nil
}

// migrate applies forward migrations to meta loaded from disk.
// Missing Version (old files) deserialises as 0 and is migrated to current.
// A version higher than the binary knows is a hard error — the user should not
// silently run an old binary against a sandbox created by a newer one.
func migrate(meta *Environment) error {
	if meta.Version > metaVersion {
		return fmt.Errorf("sandbox was created with a newer version of yoloai "+
			"(meta version %d, this binary knows %d); upgrade yoloai to use it",
			meta.Version, metaVersion)
	}
	if meta.Version < 1 {
		// v0 → v1.
		//
		// Pre-versioning sandboxes predate the `backend` field, so an empty
		// BackendType on a v0 meta means "legacy Docker sandbox" — Docker was
		// the only backend then. Backfill it explicitly here, at the migration
		// boundary, rather than coercing it at read time, so the rest of the
		// codebase can treat an empty BackendType as genuinely broken metadata.
		if meta.BackendType == "" {
			meta.BackendType = runtime.BackendDocker
		}
		// Likewise, pre-versioning sandboxes predate the `image_ref` field; the
		// only image then was the singular "yoloai-base". Backfill it so the
		// launch/restart path can trust a non-empty ImageRef.
		if meta.ImageRef == "" {
			meta.ImageRef = "yoloai-base"
		}
		// Bootstrap HostFilesystem from the backend's descriptor capability.
		// The descriptor is the single source of truth; backends declare their
		// own HostFilesystem flag (see runtime.BackendCaps).
		//
		// If the named backend isn't registered on this platform we default
		// to false — a meta whose backend can't be instantiated here will
		// fail downstream anyway, and false is the conservative answer.
		if desc, ok := runtime.Descriptor(meta.BackendType); ok {
			meta.HostFilesystem = desc.Capabilities.HostFilesystem
		}
		meta.Version = 1
	}
	if meta.Version < 2 {
		// v1 → v2: collapse the singular `workdir` field and the separate
		// `directories` slice into the ordered `dirs` list (workdir first).
		// The legacy keys unmarshalled into LegacyWorkdir/LegacyDirectories;
		// repack and clear them so they aren't written back.
		if len(meta.Dirs) == 0 && meta.LegacyWorkdir != nil {
			meta.Dirs = append([]DirEnvironment{*meta.LegacyWorkdir}, meta.LegacyDirectories...)
		}
		meta.LegacyWorkdir = nil
		meta.LegacyDirectories = nil
		meta.Version = 2
	}
	return nil
}

// SchemaVersion implements store.Record. Returns the version this binary writes.
func (e *Environment) SchemaVersion() int {
	return metaVersion
}

// MigrateRecord implements store.Migrator. Advances the record from its current
// Version to metaVersion by running the typed migration ladder. The cross-file
// v2->v3 relocation (agent/model -> agent.json; network_mode/network_allow ->
// netpolicy.json) is NOT done here — it is an explicit per-sandbox pass
// (orchestrator.MigrateAgentConfigs); this ladder only covers the in-struct
// steps (v0->v1 backend/image backfill, v1->v2 dirs collapse).
func (e *Environment) MigrateRecord() error {
	return migrate(e)
}

// MigrateEnvironment runs the in-struct migration ladder on a record loaded from
// raw bytes. Exported for the explicit `yoloai system migrate` per-sandbox pass,
// which reads a pre-v3 record's raw JSON, relocates its agent/model into
// agent.json and network_mode/network_allow into netpolicy.json, then calls
// this to bring the remaining substrate fields current before re-saving at
// metaVersion.
func MigrateEnvironment(meta *Environment) error {
	return migrate(meta)
}

// SaveEnvironment writes environment.json to the given directory path.
func SaveEnvironment(dir string, meta *Environment) error {
	meta.Version = metaVersion
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", EnvironmentFile, err)
	}

	path := filepath.Join(dir, EnvironmentFile)
	if err := fileutil.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", EnvironmentFile, err)
	}

	return nil
}

// ContainerUser returns the appropriate user string for docker exec
// operations in the given sandbox. Under container-enhanced (gVisor),
// docker exec resolves usernames from the OCI image manifest (the
// placeholder UID used at build time), not the container's live
// /etc/passwd (updated by the entrypoint's uid-remap step). Use the
// numeric host UID instead to match the remapped container user.
// hostUID is layout.HostUID at the boundary; F31's "library never
// reads os.Getuid()" discipline.
func ContainerUser(meta *Environment, hostUID int) string {
	if meta == nil {
		return "yoloai"
	}
	if meta.UsernsMode == "keep-id" {
		return ""
	}
	if meta.Isolation == runtime.IsolationModeContainerEnhanced {
		return fmt.Sprintf("%d", hostUID)
	}
	return "yoloai"
}

// LoadEnvironment reads environment.json from the given directory path. It does
// not migrate on read: a record below metaVersion balks with ErrNeedsMigration
// (the cross-file v2->v3 relocation must run via `yoloai system migrate`, D61's
// no-write-on-read rule), and a record above it errors as too-new. The version
// is read from the raw bytes BEFORE unmarshalling into the slimmed struct —
// otherwise the dropped agent/model keys would vanish silently before the
// migration could relocate them.
func LoadEnvironment(dir string) (*Environment, error) {
	path := filepath.Join(dir, EnvironmentFile)

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from sandbox dir, not user input
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", EnvironmentFile, err)
	}

	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvironmentFile, err)
	}
	switch {
	case probe.Version > metaVersion:
		return nil, fmt.Errorf("sandbox was created with a newer version of yoloai "+
			"(meta version %d, this binary knows %d); upgrade yoloai to use it",
			probe.Version, metaVersion)
	case probe.Version < metaVersion:
		return nil, fmt.Errorf("%s is at schema v%d: %w", EnvironmentFile, probe.Version, ErrNeedsMigration)
	}

	var meta Environment
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", EnvironmentFile, err)
	}

	return &meta, nil
}
