// ABOUTME: Layout — a DataDir-rooted struct exposing every yoloai data path
// ABOUTME: as a method. Replaces package-level $HOME-derived helpers (§12).

package config

import (
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// Layout names every yoloai data path rooted at a given DataDir.
// Threading a Layout through library functions, rather than reading $HOME
// implicitly, applies the no-ambient-configuration discipline
// (development-principles.md §12) to paths.
//
// Embedders construct a Layout once via NewLayout and pass it down;
// library code uses Layout methods.
type Layout struct {
	// DataDir is the root yoloai data directory; all per-Layout paths
	// derive from this. The CLI sets this to $HOME/.yoloai/ at startup
	// (its single licensed os.UserHomeDir() call); HTTP servers,
	// daemons, multi-tenant processes, and tests pass an explicit
	// path.
	//
	// Layout existence implies a non-empty DataDir: NewLayout panics
	// on empty input, and there is no other public constructor.
	// Public boundaries (yoloai.NewClient) pre-validate user
	// input and return *UsageError before reaching NewLayout, so the
	// panic is only reachable from genuinely buggy internal code.
	DataDir string

	// HomeDir is the host user's home directory — used wherever
	// library code needs to expand "~" in user-supplied paths,
	// resolve seed files (~/.claude, ~/.codex, ...), or compute
	// auth-file locations.
	//
	// HomeDir is explicit rather than derived as filepath.Dir(DataDir):
	// an embedder with a custom DataDir (e.g. /var/lib/yoloai) would
	// otherwise send seed/auth-file lookups to the wrong place. The CLI's
	// single os.UserHomeDir() call feeds this field.
	HomeDir string

	// HostUID and HostGID are the invoking user's host-side UID/GID,
	// honoring sudo (SUDO_UID / SUDO_GID when running under sudo so
	// "sudo yoloai ..." doesn't reroot to uid 0).
	//
	// Library code reads these rather than calling os.Getuid() /
	// os.Getgid() directly — the same "no ambient state" discipline as
	// HomeDir. The CLI's single licensed read (via fileutil.HostUID /
	// HostGID) feeds Layout; library never re-reads.
	HostUID int
	HostGID int

	// ProcessIsRoot is true when the running process has effective UID
	// 0 — distinct from HostUID, which honors SUDO_UID and so reads
	// the *invoking* user's UID rather than the *process's* EUID.
	//
	// Under "sudo yoloai ...", ProcessIsRoot is true but HostUID is
	// the real user's UID (non-zero). The two are needed for different
	// reasons: HostUID matches the in-container remap; ProcessIsRoot
	// answers "does this process have root privileges right now" for
	// the canRunCNIBridge check and similar.
	ProcessIsRoot bool

	// env is a snapshot of the process environment, captured once at the
	// outermost boundary (the CLI's licensed os.Environ() read in
	// cliutil). It is UNEXPORTED so no caller can grab or index the whole
	// ambient map and bypass curation: populate it only via WithEnv (at the
	// edges), and read it only through the curated HostEnv accessors reached
	// via Env() (host_env.go) — each names a purpose (EnvForDockerExec,
	// EnvForGitInvocation, EnvForConfigInterpolation, EnvForAgentCredentials, …)
	// and hands back only that purpose's keyset, so no consumer sees the whole
	// ambient map. A nil/empty map means any ${VAR} reference is an
	// unset-variable error — fine for the baked-in default config, which
	// contains none. See development-principles.md §12.
	env map[string]string

	// Principal namespaces this Layout's runtime instances so multiple
	// principals (tenants) served from one process don't collide on the
	// runtime container name. Every embedder names itself — the CLI is
	// config.CLIPrincipal ("cli"), a daemon scopes each client to its
	// tenant — so there is no default and the zero value is invalid: a
	// Layout that reaches InstanceName/InstancePrefix without a principal
	// panics (D126, superseding D62's empty sentinel). The principal is set
	// once via WithPrincipal. The Layout *is* the principal-scoped handle
	// (D58/D59); the principal is client-scoped, never per-call. See D62.
	Principal PrincipalSegment

	// SecretsStagingDir is the parent directory under which the short-lived
	// per-sandbox secrets directory is created (one file per credential,
	// bind-mounted into the container and removed seconds after startup). The
	// zero value "" means "use Layout.MkdirTemp" (DataDir/tmp), keeping
	// yoloai's footprint entirely under the data dir. A daemon embedder
	// serving multiple principals can point this at a per-principal tmpfs so
	// one principal's plaintext credentials are never staged on a path another
	// principal can read. This is the "the *what* stays in the library, the
	// *where* becomes caller-supplied" refinement of D59 — the library still
	// decides what to stage and when to delete it; the embedder decides where.
	SecretsStagingDir string
}

// TempDir returns DataDir/tmp — the yoloai-owned scratch root. Temp files live
// here, not the global temp dir, so yoloai's entire on-disk footprint stays
// localized under one relocatable data dir (two installs with different data
// dirs never share scratch space).
func (l Layout) TempDir() string { return filepath.Join(l.DataDir, "tmp") }

// MkdirTemp creates a uniquely-named directory under TempDir() (creating
// TempDir() if needed) and returns its path. The yoloai-rooted analogue of
// os.MkdirTemp(""), used everywhere that previously wrote to the global temp dir.
func (l Layout) MkdirTemp(pattern string) (string, error) {
	if err := fileutil.MkdirAll(l.TempDir(), 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(l.TempDir(), pattern)
}

// CreateTemp is the file analogue of MkdirTemp.
func (l Layout) CreateTemp(pattern string) (*os.File, error) {
	if err := fileutil.MkdirAll(l.TempDir(), 0o700); err != nil {
		return nil, err
	}
	return os.CreateTemp(l.TempDir(), pattern)
}

// WithPrincipal returns a copy of the Layout scoped to the given principal.
// The principal must be non-empty (D126) — it is validated at the boundary via
// ParsePrincipalSegment; a Layout left unprincipaled panics when an instance
// name is derived from it. The principal is set in exactly one place per client,
// so every InstanceName derived from this Layout shares the same scope.
func (l Layout) WithPrincipal(p PrincipalSegment) Layout {
	l.Principal = p
	return l
}

// NewLayout constructs a Layout rooted at dataDir with HomeDir
// derived as the conventional parent of dataDir. This derivation is
// only correct when dataDir lives directly inside $HOME (e.g.
// $HOME/.yoloai) — the convention used by internal tests. It is NOT
// valid for the D60-bifurcated public DataDir ($HOME/.yoloai/library,
// whose parent is $HOME/.yoloai, not $HOME); the public boundaries
// (yoloai.NewClient / Client.System) therefore require an
// explicit HomeDir and always call NewLayoutFor. Use NewLayoutFor
// directly whenever DataDir and HomeDir differ.
//
// Panics if dataDir is empty. Callers pre-validate against *UsageError
// before constructing a Layout, so empty here is a programming bug
// (Q-X: bugs panic, user errors return typed errors). F14: this enforces
// the "Layout existence ⇒ valid DataDir" invariant at the type-
// construction boundary instead of duplicating the check in every
// Engine/Client method.
func NewLayout(dataDir string) Layout {
	if dataDir == "" {
		panic("config.NewLayout: dataDir is required (empty string is invalid; public boundaries must validate input and return *UsageError before reaching this constructor)")
	}
	return Layout{
		DataDir:       dataDir,
		HomeDir:       filepath.Dir(dataDir),
		HostUID:       fileutil.HostUID(),
		HostGID:       fileutil.HostGID(),
		ProcessIsRoot: fileutil.ProcessIsRoot(),
	}
}

// NewLayoutFor constructs a Layout with an explicit HomeDir. Used by
// callers whose DataDir isn't a subdirectory of HomeDir (e.g. system-
// service installs where DataDir = /var/lib/yoloai but the user's
// $HOME is elsewhere). Panics on empty input — same Q-X discipline as
// NewLayout. HostUID / HostGID are populated from fileutil.HostUID /
// HostGID (the F31 chokepoint); use Layout{} literals when a test
// needs fully explicit fields.
func NewLayoutFor(dataDir, homeDir string) Layout {
	if dataDir == "" {
		panic("config.NewLayoutFor: dataDir is required")
	}
	if homeDir == "" {
		panic("config.NewLayoutFor: homeDir is required")
	}
	return Layout{
		DataDir:       dataDir,
		HomeDir:       homeDir,
		HostUID:       fileutil.HostUID(),
		HostGID:       fileutil.HostGID(),
		ProcessIsRoot: fileutil.ProcessIsRoot(),
	}
}

// YoloaiDir returns the root data directory (an alias for DataDir).
func (l Layout) YoloaiDir() string { return l.DataDir }

// SandboxesDir returns DataDir/sandboxes/.
func (l Layout) SandboxesDir() string {
	return filepath.Join(l.DataDir, "sandboxes")
}

// ProfilesDir returns DataDir/profiles/.
func (l Layout) ProfilesDir() string {
	return filepath.Join(l.DataDir, "profiles")
}

// CacheDir returns DataDir/cache/.
func (l Layout) CacheDir() string {
	return filepath.Join(l.DataDir, "cache")
}

// TrashDir returns DataDir/trash/, the quarantine location for broken
// sandbox directories that prune cannot confidently classify as junk
// (e.g. unreadable or version-too-new metadata). Quarantining instead
// of deleting keeps repair reversible: the user restores with a plain
// `mv`, and `system prune` only empties trash after explicit consent.
func (l Layout) TrashDir() string {
	return filepath.Join(l.DataDir, "trash")
}

// DefaultsDir returns DataDir/defaults/.
func (l Layout) DefaultsDir() string {
	return filepath.Join(l.DataDir, "defaults")
}

// DefaultsConfigPath returns DataDir/defaults/config.yaml.
func (l Layout) DefaultsConfigPath() string {
	return filepath.Join(l.DefaultsDir(), "config.yaml")
}

// TartBaseMetadataDir returns the directory for Tart runtime base
// metadata under this layout.
func (l Layout) TartBaseMetadataDir() string {
	return filepath.Join(l.DataDir, "tart-base-metadata")
}

// TartBaseLocksDir returns the directory for Tart runtime base locks
// under this layout.
func (l Layout) TartBaseLocksDir() string {
	return filepath.Join(l.DataDir, "tart-base-locks")
}

// DockerBaseLocksDir returns the directory for Docker base-image
// build locks under this layout.
func (l Layout) DockerBaseLocksDir() string {
	return filepath.Join(l.DataDir, "docker-base-locks")
}

// AgentsDir returns DataDir/agents/ — the directory where user-defined
// agent YAML files are placed (one file per agent, named <type>.yaml).
// A missing directory is silently ignored by RegisterFileAgents.
func (l Layout) AgentsDir() string {
	return filepath.Join(l.DataDir, "agents")
}

// VscodeCLIDir returns DataDir/vscode-cli/, the global VS Code CLI
// token seed store. It is NOT mounted directly into containers;
// each sandbox gets its own per-sandbox vscode-cli directory
// (seeded from this location on first use) to prevent VS Code CLI's
// singleton lock from blocking concurrent tunnels.
func (l Layout) VscodeCLIDir() string {
	return filepath.Join(l.DataDir, "vscode-cli")
}

// SandboxDir returns the per-sandbox state directory:
// DataDir/sandboxes/<name>/. Equivalent to store.Dir(name) under the
// legacy package-level helpers; the migration target for the 42+
// store.Dir call sites (Q-W.4b).
func (l Layout) SandboxDir(name string) string {
	return filepath.Join(l.SandboxesDir(), name)
}

// SandboxLockPath returns the per-sandbox advisory lockfile path:
// DataDir/sandboxes/<name>.lock. The lockfile lives next to the
// sandbox dir (not inside it) so it works before the sandbox
// directory is created — e.g. during "yoloai new".
func (l Layout) SandboxLockPath(name string) string {
	return filepath.Join(l.SandboxesDir(), name+".lock")
}

// TartBaseLockPath returns the lockfile path for serializing Tart
// base VM builds: DataDir/tart-base-locks/<baseName>.lock.
func (l Layout) TartBaseLockPath(baseName string) string {
	return filepath.Join(l.TartBaseLocksDir(), baseName+".lock")
}

// DockerBaseLockPath returns the lockfile path for serializing
// Docker base image builds: DataDir/docker-base-locks/<baseName>.lock.
func (l Layout) DockerBaseLockPath(baseName string) string {
	return filepath.Join(l.DockerBaseLocksDir(), baseName+".lock")
}

// GlobalConfigPath returns DataDir/config.yaml — the user-level
// yoloai configuration file. Migration target for the package-level
// GlobalConfigPath() helper.
func (l Layout) GlobalConfigPath() string {
	return filepath.Join(l.DataDir, "config.yaml")
}

// ProfileDir returns DataDir/profiles/<name>/. Migration target for
// the package-level ProfileDirPath(name) helper.
func (l Layout) ProfileDir(name string) string {
	return filepath.Join(l.ProfilesDir(), name)
}

// CniDir returns DataDir/cni/ — the containerd backend's per-data-dir
// CNI configuration directory.
func (l Layout) CniDir() string {
	return filepath.Join(l.DataDir, "cni")
}

// SchemaVersionPath returns DataDir/.schema-version — the stamp that
// records which on-disk schema version the library's DataDir was last
// migrated to. Its presence (and value) is the deterministic signal
// the library's migration runner keys off; see MigrateLibrary.
func (l Layout) SchemaVersionPath() string {
	return filepath.Join(l.DataDir, ".schema-version")
}
