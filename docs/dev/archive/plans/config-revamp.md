# Config System Revamp Implementation Plan

Design spec: [`docs/design/config.md`](../../design/config.md)

Implements the profile system redesign: `profiles/base/` → `defaults/`, self-contained
profiles, baked-in defaults, `os` field, unknown field validation, two distinct load paths.

This is a breaking change — update `BREAKING-CHANGES.md` before shipping.

---

## Phase 1: Baked-in defaults YAML and scaffold generation

Establish the single source of truth for defaults and the generation of the user-facing
scaffold config. No load path changes yet.

### Step 1.1 — `config/defaults.go`: replace stubs with full baked-in YAML

Replace `DefaultConfigYAML` (currently a near-empty stub) with a complete YAML document
containing every profile/defaults setting, its actual default value, and inline documentation
comments. Every key must be present and uncommented. This is the authoritative source of
defaults that all merge paths fall back to.

Structure the YAML with section comments for readability:

```yaml
# --- Agent ---

# Agent to launch inside the sandbox.
# Valid values: aider, claude, codex, gemini, opencode
# CLI --agent overrides.
agent: claude

# Model name or alias passed to the agent. Empty = agent's own default.
# CLI --model overrides.
model: ""

# --- Runtime ---

# Guest OS for the sandbox. Valid values: linux (default), mac.
#   os=linux: Docker or Podman for Linux containers; containerd for vm/vm-enhanced
#   os=mac: Seatbelt (container isolation) or Tart (vm isolation); requires macOS host
# CLI --os overrides.
os: linux

# Preferred Linux container backend. Valid values: docker, podman, or "" (auto-detect).
# Both work on Linux and macOS. Ignored for vm/vm-enhanced (uses containerd)
# and os=mac (uses Seatbelt or Tart). CLI --backend overrides.
# Empty string (default): auto-detect — prefers docker over podman if both are present.
container_backend: ""

# Isolation level for the sandbox.
# Valid values: container, container-enhanced, vm, vm-enhanced
#   container:          os=linux: Docker or Podman; os=mac: Seatbelt
#   container-enhanced: os=linux: Podman required; os=mac: not supported
#   vm:                 os=linux: KVM + Kata required; os=mac: Tart required
#   vm-enhanced:        os=linux: KVM + Kata + Firecracker required; os=mac: not supported
# CLI --isolation overrides.
isolation: container

# --- Tart (macOS VM backend) ---

# Custom base VM image for the Tart backend (os=mac, isolation=vm).
tart:
  image: ""

# --- Network ---

# Network isolation settings.
network:
  # Set to true to enable network isolation for all sandboxes.
  isolated: false
  # Additional domains to allow when isolation is active (additive with agent defaults).
  allow: []

# --- Files and mounts ---

# Files copied into the sandbox's agent-state directory on first run.
# String form: base directory (agent subdir appended), e.g. "${HOME}"
# List form:   specific files or dirs, e.g. ["~/.claude/settings.json"]
# Omit to copy nothing (safe default).
# WARNING: ${HOME} and other env vars are expanded at runtime — values differ per machine.
# agent_files: ""

# Extra bind mounts added at container run time.
# Format: host-path:container-path[:ro]
# WARNING: paths are machine-specific. Personal mounts belong in defaults/, not profiles.
mounts: []

# --- Ports and resources ---

# Default port mappings. Format: host-port:container-port
ports: []

# Container resource limits.
resources:
  cpus: ""
  memory: ""

# --- Agent behaviour ---

# Per-agent default CLI args inserted before -- passthrough args.
# Set per agent: agent_args.aider, agent_args.claude, etc.
agent_args: {}

# Environment variables forwarded to the container via /run/secrets/.
# Supports ${VAR} expansion. WARNING: expanded values are machine-specific.
env: {}

# Seconds between automatic git commits in :copy directories. 0 = disabled.
auto_commit_interval: 0

# --- Advanced ---

# Linux capabilities to add (Docker/Podman only).
cap_add: []

# Host devices to expose (Docker/Podman only).
devices: []

# Commands to run at container start before the agent launches.
setup: []
```

Also add a `GenerateScaffoldConfig` function that produces the user-facing scaffold by
commenting out all uncommented, non-blank lines. The `container_backend` default in the
baked-in YAML is `""` (empty string = auto-detect via `detectContainerBackend()`), matching
existing behavior — this is NOT changing to `"docker"` as that would silently break
Podman-only users on upgrade. Auto-detect prefers docker over podman if both are present.

```go
// GenerateScaffoldConfig takes the baked-in defaults YAML and returns a version
// where every non-blank, non-comment line is commented out. The result is suitable
// for writing as defaults/config.yaml on first run — self-documenting but inert
// until the user uncomments and edits specific settings.
func GenerateScaffoldConfig(bakedInYAML string) string {
    var out strings.Builder
    for _, line := range strings.Split(bakedInYAML, "\n") {
        trimmed := strings.TrimSpace(line)
        if trimmed == "" || strings.HasPrefix(trimmed, "#") {
            out.WriteString(line + "\n")
        } else {
            out.WriteString("# " + line + "\n")
        }
    }
    return out.String()
}
```

Keep `DefaultGlobalConfigYAML` — the global config (tmux_conf, model_aliases) is small and
doesn't need this treatment.

### Step 1.2 — `config/config.go`: add `OS` field, remove `Profile`, add `DefaultsDir`

**`YoloaiConfig` struct** — add `OS`, remove `Profile`:

```go
type YoloaiConfig struct {
    OS                 string            `yaml:"os"`
    ContainerBackend   string            `yaml:"container_backend"`
    // ... existing fields ...
    // remove: Profile string `yaml:"profile"`
}
```

**`knownSettings`** — add `os`, remove `profile`:

```go
var knownSettings = []knownSetting{
    {"os", "linux"},
    {"container_backend", "docker"},
    {"agent", "claude"},
    {"model", ""},
    {"isolation", "container"},
    // remove: {"profile", ""},
    // ... rest unchanged ...
}
```

**New path helpers:**

```go
// DefaultsDir returns the path to ~/.yoloai/defaults/.
func DefaultsDir() string {
    return filepath.Join(YoloaiDir(), "defaults")
}

// DefaultsConfigPath returns the path to ~/.yoloai/defaults/config.yaml.
func DefaultsConfigPath() string {
    return filepath.Join(DefaultsDir(), "config.yaml")
}
```

**`ConfigPath()`** — update to point to defaults:

```go
// ConfigPath returns the path to ~/.yoloai/defaults/config.yaml.
// Used by config get/set/reset for non-global settings.
func ConfigPath() string {
    return DefaultsConfigPath()
}
```

**Parse loop in `LoadConfig()`** — add `os` case, remove `profile` case:

```go
case "os":
    expanded, err := expandEnvBraced(val.Value)
    if err != nil {
        return nil, fmt.Errorf("os: %w", err)
    }
    cfg.OS = expanded
```

---

## Phase 2: Two load paths and unknown field validation

Implement the two distinct merge paths (with-defaults, with-profile) and error on unknown
fields.

### Step 2.1 — `config/config.go`: `LoadBakedInDefaults()`

Add a function that parses the baked-in defaults YAML into a `YoloaiConfig`. This is the base
for both merge paths:

```go
// LoadBakedInDefaults parses the embedded defaults YAML into a YoloaiConfig.
// Returns a fully-populated config with every field at its baked-in default.
func LoadBakedInDefaults() (*YoloaiConfig, error) {
    // Temporarily assign DefaultConfigYAML to a var; parse via the same
    // logic as LoadConfig() but from the embedded string rather than a file.
    return parseConfigYAML([]byte(DefaultConfigYAML), "<baked-in>")
}
```

Extract the parsing logic from `LoadConfig()` into a shared `parseConfigYAML(data []byte, source string) (*YoloaiConfig, error)` helper used by both `LoadBakedInDefaults()` and the file-based loaders.

### Step 2.2 — `config/config.go`: `LoadDefaultsConfig()` and merge

Rename/supplement `LoadConfig()` with a function that implements the no-profile load path:
baked-in defaults merged with `defaults/config.yaml`.

```go
// LoadDefaultsConfig loads the effective config for the no-profile path:
// baked-in defaults merged with ~/.yoloai/defaults/config.yaml.
// Used by sandbox.Create() when no --profile is given.
func LoadDefaultsConfig() (*YoloaiConfig, error) {
    base, err := LoadBakedInDefaults()
    if err != nil {
        return nil, err
    }

    data, err := os.ReadFile(DefaultsConfigPath())
    if err != nil {
        if os.IsNotExist(err) {
            return base, nil
        }
        return nil, fmt.Errorf("read defaults/config.yaml: %w", err)
    }

    override, err := parseConfigYAML(data, DefaultsConfigPath())
    if err != nil {
        return nil, err
    }

    return mergeConfigs(base, override), nil
}
```

**`mergeConfigs(base, override *YoloaiConfig) *YoloaiConfig`** — implements the merge table
from the design doc (scalars override, lists are additive, maps merge, `agent_files` replaces).
Add as a new function in `config/config.go`.

### Step 2.3 — `config/profile.go`: `LoadProfileConfig()` and full profile.go cleanup

`profile.go` currently (671 lines) has a `base → profile` chain system that must be fully
removed. This step is one of the largest in the plan. Enumerate every change needed:

**A. Rename `profile.yaml` → `config.yaml`** throughout:

- `LoadProfile()`: change `filepath.Join(profileDir, "profile.yaml")` → `"config.yaml"`
- `ProfileExists()`: change filename check
- `ListProfiles()`: change filename check (only lists dirs that contain the config file)
- Any other place the string `"profile.yaml"` appears — search and replace all.

**B. Remove `Extends` chain resolution:**

- Remove `Extends string` field from `ProfileConfig`
- Remove `ResolveProfileChain()` function entirely (walks `extends:` chain, cycle detection)
- Remove `formatCycle()` helper used only by `ResolveProfileChain()`
- Remove the chain-walking loop in `MergeProfileChain()` — it becomes a simple
  two-config merge (baked-in + single profile config)

**C. Add `OS` field to `ProfileConfig` and `MergedConfig`:**

```go
type ProfileConfig struct {
    OS                string            `yaml:"os"`
    ContainerBackend  string            `yaml:"container_backend"`
    // ... (Backend field already exists; verify it maps to container_backend)
    // ... rest of fields unchanged
}

type MergedConfig struct {
    OS                string
    ContainerBackend  string
    // ... rest of fields unchanged
}
```

**D. Remove `"base"` reservation from `ValidateProfileName()`:**

The old code rejects `"base"` as a profile name (reserved for the base profile). Under the
new design there is no reserved base profile — remove this check. `ValidateProfileName()`
should only reject empty strings and names containing path separators.

**E. Remove `"base"` exclusion from `ListProfiles()`:**

The old code filters out `"base"` from the list. Remove this filter. After migration, users
who still have `profiles/base/` will see it listed — that is intentional (migration message
tells them to delete it).

**F. Update `LoadMergedConfig()` to use baked-in defaults as base:**

```go
// Before:
func LoadMergedConfig(profileName string) (*MergedConfig, error) {
    base, err := LoadConfig()            // reads defaults/config.yaml
    chain, err := ResolveProfileChain(profileName)
    return MergeProfileChain(base, chain)
}

// After:
func LoadMergedConfig(profileName string) (*MergedConfig, error) {
    cfg, err := LoadProfileConfig(profileName)  // baked-in + single profile
    if err != nil {
        return nil, err
    }
    return configToMergedConfig(cfg), nil
}
```

**G. New `LoadProfileConfig()` implementation:**

```go
// LoadProfileConfig loads the effective config for the with-profile path:
// baked-in defaults merged with ~/.yoloai/profiles/<name>/config.yaml.
// defaults/config.yaml is NOT consulted — profiles are self-contained.
func LoadProfileConfig(name string) (*YoloaiConfig, error) {
    base, err := config.LoadBakedInDefaults()
    if err != nil {
        return nil, err
    }

    profileConfigPath := filepath.Join(config.ProfilesDir(), name, "config.yaml")
    data, err := os.ReadFile(profileConfigPath)
    if err != nil {
        if os.IsNotExist(err) {
            return base, nil
        }
        return nil, fmt.Errorf("read profile config: %w", err)
    }

    override, err := config.ParseConfigYAML(data, profileConfigPath, config.KnownProfileKeys)
    if err != nil {
        return nil, err
    }

    return config.MergeConfigs(base, override), nil
}
```

**H. `MergeProfileChain()` as the template for `MergeConfigs()`:**

The existing `MergeProfileChain()` already implements the correct merge semantics
(scalars override, lists are additive, maps merge, `agent_files` replaces). Use it as
the direct template when writing `MergeConfigs(base, override *YoloaiConfig) *YoloaiConfig`
in `config/config.go`. The merge rules do not change — only the call site changes from a
chain to a two-config merge.

### Step 2.4 — Unknown field validation

`workdir` and `directories` are valid in profile configs (they set per-profile defaults for
the sandbox directory layout) but meaningless in `defaults/config.yaml` (they are ignored
at runtime). Rather than silently accepting them in defaults, treat them as errors — a user
who accidentally puts `workdir:` in their defaults config has made a mistake.

This requires **two separate key sets** and a `knownKeys` parameter on `parseConfigYAML()`:

```go
// knownDefaultsKeys: valid in defaults/config.yaml
var knownDefaultsKeys = map[string]bool{
    "os": true, "agent": true, "model": true, "container_backend": true,
    "isolation": true, "tart": true, "network": true, "agent_files": true,
    "mounts": true, "ports": true, "resources": true, "agent_args": true,
    "env": true, "auto_commit_interval": true, "cap_add": true,
    "devices": true, "setup": true,
}

// knownProfileKeys: valid in profiles/<name>/config.yaml (superset of defaults keys)
var knownProfileKeys = map[string]bool{
    "os": true, "agent": true, "model": true, "container_backend": true,
    "isolation": true, "tart": true, "network": true, "agent_files": true,
    "mounts": true, "ports": true, "resources": true, "agent_args": true,
    "env": true, "auto_commit_interval": true, "cap_add": true,
    "devices": true, "setup": true,
    "workdir": true, "directories": true, // profile-only
}
```

Update the signature of `parseConfigYAML()` to accept the key set:

```go
func parseConfigYAML(data []byte, source string, knownKeys map[string]bool) (*YoloaiConfig, error)
```

After parsing the root mapping, check for unknown keys:

```go
var unknown []string
for i := 0; i < len(root.Content)-1; i += 2 {
    key := root.Content[i].Value
    if !knownKeys[key] {
        unknown = append(unknown, key)
    }
}
if len(unknown) > 0 {
    sort.Strings(unknown)
    return nil, fmt.Errorf("%s: unknown config field(s): %s", source, strings.Join(unknown, ", "))
}
```

- `LoadBakedInDefaults()` passes `knownDefaultsKeys` (baked-in YAML must only contain defaults keys).
- `LoadDefaultsConfig()` passes `knownDefaultsKeys` when parsing the user's `defaults/config.yaml`.
- `LoadProfileConfig()` passes `knownProfileKeys` when parsing a profile's `config.yaml`.

### Step 2.5 — `GetEffectiveConfig()` update

Update `GetEffectiveConfig()` to use `LoadDefaultsConfig()` rather than reading
`profiles/base/config.yaml`. The displayed effective config should reflect baked-in
defaults merged with `defaults/config.yaml` (or the named profile's config, if a
`--profile` flag is added in future).

---

## Phase 3: First-run setup and migration

### Step 3.1 — `EnsureSetup()`: create `defaults/` scaffold

Update `EnsureSetup()` / `EnsureSetupNonInteractive()` in `sandbox/manager.go`:

1. Create `~/.yoloai/defaults/` directory (mode 0750) if absent.
2. If `defaults/config.yaml` does not exist, write it using `GenerateScaffoldConfig(DefaultConfigYAML)`.
3. **Do not** write Dockerfile or entrypoint scripts to `defaults/` — these are baked-in.
4. `defaults/tmux.conf` is written by the interactive setup wizard (`yoloai setup`) based on
   the user's existing host tmux config. It is not written during non-interactive
   `EnsureSetupNonInteractive()` — only when the user explicitly runs setup.

```go
func ensureDefaultsDir() error {
    defaultsDir := config.DefaultsDir()
    if err := os.MkdirAll(defaultsDir, 0750); err != nil {
        return fmt.Errorf("create defaults dir: %w", err)
    }
    configPath := config.DefaultsConfigPath()
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        scaffold := config.GenerateScaffoldConfig(config.DefaultConfigYAML)
        if err := os.WriteFile(configPath, []byte(scaffold), 0600); err != nil {
            return fmt.Errorf("write defaults/config.yaml: %w", err)
        }
    }
    return nil
}
```

Call `ensureDefaultsDir()` from `EnsureSetupNonInteractive()` before the image build step.

### Step 3.2 — `config/migration.go`: guard against missing `defaults/`

Existing users upgrading from the old layout will have `profiles/base/config.yaml` with their
customizations and no `defaults/`. Rather than silently auto-migrating (which could bring in
now-invalid keys like `profile:`), detect this case and error with clear instructions.

**Gating on `setup_complete`:**

There is a sequencing problem: `CheckDefaultsDir()` must NOT fire on fresh installs, because
fresh installs don't have `defaults/` yet either — `ensureDefaultsDir()` hasn't created it
yet. The distinguishing signal is `setup_complete` in `state.yaml`:

- **Fresh install** (`setup_complete == false`): skip `CheckDefaultsDir()`, let `ensureDefaultsDir()` create the scaffold.
- **Returning user** (`setup_complete == true`, `defaults/` missing): fire `CheckDefaultsDir()` → error with migration instructions.
- **Returning user** (`setup_complete == true`, `defaults/` present): `CheckDefaultsDir()` → no-op, proceed normally.

The call order in `EnsureSetupNonInteractive()`:

```go
func (m *Manager) EnsureSetupNonInteractive(ctx context.Context) error {
    state, err := config.LoadState()
    if err != nil {
        return err
    }

    // Upgrading user: defaults/ should exist. If it doesn't, they need to migrate.
    if state.SetupComplete {
        if err := config.CheckDefaultsDir(); err != nil {
            return err
        }
    }

    // Fresh install (or after manual migration): create defaults/ scaffold.
    if err := ensureDefaultsDir(); err != nil {
        return err
    }

    // ... rest of setup (image build, etc.) ...
}
```

`yoloai setup` (interactive) always calls `ensureDefaultsDir()` without gating on
`setup_complete` — that is "Option 1" for existing users who want a clean start.

**`CheckDefaultsDir()` implementation:**

```go
// CheckDefaultsDir verifies that ~/.yoloai/defaults/ exists. If it doesn't,
// returns a descriptive error telling the user how to resolve it.
// Only called when setup_complete is true (i.e., this is an upgrade, not a fresh install).
func CheckDefaultsDir() error {
    if _, err := os.Stat(DefaultsDir()); err == nil {
        return nil // exists, nothing to do
    }
    msg := "~/.yoloai/defaults/ not found\n\n" +
        "This directory was added in a recent update. To fix:\n\n" +
        "  Option 1 — Re-run setup (creates a fresh defaults/config.yaml):\n" +
        "    yoloai setup\n\n" +
        "  Option 2 — Copy your existing settings manually:\n" +
        "    mkdir -p ~/.yoloai/defaults\n" +
        "    cp ~/.yoloai/profiles/base/config.yaml ~/.yoloai/defaults/config.yaml\n" +
        "  Then remove any 'profile:' line from the copied file (that key no longer exists).\n\n" +
        "  Note: after migration, 'base' will appear as a regular profile in 'yoloai profile list'.\n" +
        "  You may want to remove it: yoloai profile delete base\n"
    return NewConfigError(msg)
}
```

---

## Phase 4: CLI wiring

### Step 4.1 — `internal/cli/config.go`: update help text

Update all three command help strings to reference `defaults/config.yaml` instead of
`profiles/base/config.yaml`. No logic changes needed — `ConfigPath()` already returns the
new path after Phase 1.

### Step 4.2 — `internal/cli/config.go`: `config set` — don't create `{}` stub

Currently `config set` creates `{}\n` if the file doesn't exist. After the revamp, the
scaffold file is created by `EnsureSetup()` and will always exist by the time `config set`
is called. Remove the stub-creation logic; if the file is somehow absent, write the scaffold
instead:

```go
if _, err := os.Stat(configPath); os.IsNotExist(err) {
    scaffold := config.GenerateScaffoldConfig(config.DefaultConfigYAML)
    if err := os.WriteFile(configPath, []byte(scaffold), 0600); err != nil {
        return fmt.Errorf("create defaults/config.yaml: %w", err)
    }
}
```

### Step 4.3 — `internal/cli/helpers.go`: `resolveBackend()` reads `os` and `isolation` from config

Update `resolveBackend()` to use the effective config as the fallback for `os` and
`isolation` before CLI flags (per the containerd plan Step 1.8 update):

```go
func resolveBackend(cmd *cobra.Command) (string, bool) {
    cfg, _ := config.LoadDefaultsConfig() // or LoadProfileConfig if --profile given
    isolation := coalesce(flagStr(cmd, "isolation"), cfg.Isolation)
    targetOS  := coalesce(flagStr(cmd, "os"),        cfg.OS)
    backendFlag, _ := cmd.Flags().GetString("backend")
    return resolveBackendFull(isolation, targetOS, backendFlag, backendFlag != "")
}

func coalesce(a, b string) string {
    if a != "" {
        return a
    }
    return b
}
```

### Step 4.4 — Remove `profile` config key support

- Remove `profile` from `YoloaiConfig`, `knownSettings`, and the parse loop (Phase 1 handles this).
- Remove any code in `sandbox/create.go` or `internal/cli/commands.go` that reads
  `cfg.Profile` to set a default profile. The `--profile` flag on `yoloai new` is always
  explicit; there is no config-level default.

### Step 4.5 — `docs/BREAKING-CHANGES.md`

The breaking change entry is already written. Verify it covers:
- `profiles/base/config.yaml` → `defaults/config.yaml`
- `profile` config key removed
- Unknown fields now error (was silently ignored)
- `os` field added

---

## Phase 5: Remove `SeedResources()` writes to `profiles/base/`

`runtime/docker/build.go`'s `SeedResources()` currently writes 7 files (Dockerfile,
`entrypoint.sh`, `entrypoint.py`, `sandbox-setup.py`, `status-monitor.py`,
`diagnose-idle.sh`, `tmux.conf`) to `~/.yoloai/profiles/base/`. The image build functions
then read those files back **from disk** — they are not using the embedded vars directly.
This means `SeedResources()` is load-bearing: skip it and the build breaks.

The fix is to change the build to read from embedded vars rather than from disk:

### Step 5.1 — `createBuildContext()`: use embedded vars instead of disk reads

`createBuildContext(sourceDir string)` currently calls `os.ReadFile(path)` for each file
under `sourceDir`. Replace these reads with direct references to the embedded vars already
defined in `resources.go` (e.g., `embeddedDockerfile`, `embeddedEntrypoint`, etc.):

```go
// Before:
content, err := os.ReadFile(filepath.Join(sourceDir, "Dockerfile"))

// After:
content := embeddedDockerfile  // []byte from resources.go
```

Map each filename to its corresponding embedded variable. The tar archive written by
`createBuildContext()` remains structurally the same — only the data source changes.

### Step 5.2 — `buildInputsChecksum()`: hash embedded vars instead of disk reads

`buildInputsChecksum()` hashes the files under `sourceDir` to detect when a rebuild is
needed. Replace each `os.ReadFile()` with the corresponding embedded var:

```go
// hash each embedded resource in a deterministic order
for _, content := range [][]byte{
    embeddedDockerfile,
    embeddedEntrypoint,
    embeddedEntrypointPy,
    // ... etc
} {
    h.Write(content)
}
```

`NeedsBuild()` calls `buildInputsChecksum()` — once Step 5.2 is done, `NeedsBuild()` no
longer needs `sourceDir` either.

### Step 5.3 — Update callers and remove `SeedResources()`

- `EnsureImage(sourceDir string)` is called from `sandbox/manager.go` with `baseProfileDir`.
  Once Steps 5.1–5.2 are done, `sourceDir` is unused — remove the parameter or pass `""`.
- Remove the `SeedResources()` call from `EnsureSetupNonInteractive()` in `sandbox/manager.go`.
- Remove the `SeedResources()` function from `build.go` entirely.
- Remove the `baseProfileDir` variable and the `os.MkdirAll(baseProfileDir, ...)` call from
  `EnsureSetupNonInteractive()`.

**Checkpoint:** after this phase, `~/.yoloai/profiles/base/` has no write path in the new
code. Existing `profiles/base/` directories on user machines are handled by migration (Phase 3.2).

---

## Phase 6: Tests and documentation

### Step 6.1 — Unit tests

**`config/config_test.go`:**
- `TestLoadBakedInDefaults` — parses without error; `OS == "linux"`, `Agent == "claude"`, etc.
- `TestGenerateScaffoldConfig` — every uncommented value line becomes commented; comment and blank lines pass through.
- `TestParseConfigYAML_UnknownField` — returns error naming the unknown key.
- `TestMergeConfigs_*` — scalar override, list additive, map merge, agent_files replace.
- `TestLoadDefaultsConfig_NoFile` — returns baked-in defaults when file absent.
- `TestLoadDefaultsConfig_WithOverrides` — file values win over baked-in defaults.

**`config/profile_test.go`** (update existing):
- `TestLoadProfileConfig_DoesNotUseDefaults` — profile config does not pick up values set in `defaults/config.yaml`.
- `TestLoadProfileConfig_UnknownField` — returns error for unknown field.

**`config/migration_test.go`:**
- `TestCheckDefaultsDir_Exists` — no error when `defaults/` is present.
- `TestCheckDefaultsDir_Missing` — returns `ConfigError` with both options in the message.
- `TestCheckDefaultsDir_MissingWithOldBase` — error message mentions `profiles/base/config.yaml` copy step.

### Step 6.2 — Update `docs/dev/ARCHITECTURE.md`

- `cli/config.go` description: update path reference from `profiles/base/config.yaml` to `defaults/config.yaml`.
- `runtime/docker/resources.go` description: remove `SeedResources()` reference.
- Host Directory Layout: replace `profiles/base/` block with `defaults/` block.
- "Change container setup" recipe: remove reference to `profiles/base/`.
- "Change config handling" recipe: update path and note two load paths.
- `sandbox.ProfileConfig / sandbox.MergedConfig` description: update to reflect new inheritance.

---

## File Change Summary

### Phase 1

| File | Change |
|------|--------|
| `config/defaults.go` | Replace `DefaultConfigYAML` stub with full baked-in YAML + inline docs. Add `GenerateScaffoldConfig()`. |
| `config/config.go` | Add `OS` to `YoloaiConfig`. Remove `Profile`. Add `DefaultsDir()`, `DefaultsConfigPath()`. Update `ConfigPath()`. Update `knownSettings`. Add `os` parse case, remove `profile` parse case. |

### Phase 2

| File | Change |
|------|--------|
| `config/config.go` | Extract `parseConfigYAML(data, source, knownKeys)`. Add `LoadBakedInDefaults()`, `LoadDefaultsConfig()`, `MergeConfigs()`. Add `knownDefaultsKeys` + `knownProfileKeys` sets and unknown-field validation. Update `GetEffectiveConfig()`. |
| `config/profile.go` | Rename `profile.yaml` → `config.yaml` throughout. Remove `Extends` field, `ResolveProfileChain()`, `formatCycle()`, chain loop. Add `OS` to `ProfileConfig`/`MergedConfig`. Remove `"base"` reservation from `ValidateProfileName()` and exclusion from `ListProfiles()`. Update `LoadMergedConfig()` base to baked-in defaults. Add `LoadProfileConfig()`. Derive `MergeConfigs()` from existing `MergeProfileChain()` logic. |

### Phase 3

| File | Change |
|------|--------|
| `sandbox/manager.go` | Add `ensureDefaultsDir()`. Call from `EnsureSetupNonInteractive()`. Gate `CheckDefaultsDir()` on `state.SetupComplete`. |
| `config/migration.go` | **New.** `CheckDefaultsDir()` — errors with migration instructions if `defaults/` absent (only called when `setup_complete == true`). |

### Phase 4

| File | Change |
|------|--------|
| `internal/cli/config.go` | Update help text. Replace `{}\n` stub with scaffold write. |
| `internal/cli/helpers.go` | `resolveBackend()` reads `os` and `isolation` from effective config as fallback. Add `coalesce()` helper. |
| `config/config.go` | Remove `Profile` field (Phase 1). Verify no remaining `cfg.Profile` references. |
| `sandbox/create.go` | Remove any `cfg.Profile` usage for default profile selection. |

### Phase 5

| File | Change |
|------|--------|
| `runtime/docker/build.go` | `createBuildContext()` and `buildInputsChecksum()` use embedded vars instead of disk reads. Remove `SeedResources()`. Update `EnsureImage()` signature. |
| `sandbox/manager.go` | Remove `SeedResources()` call and `baseProfileDir` setup. |

### Phase 6

| File | Change |
|------|--------|
| `config/config_test.go` | New tests for baked-in defaults, scaffold gen, unknown fields, merging. |
| `config/profile_test.go` | Update: profile does not inherit from defaults; unknown field errors. |
| `config/migration_test.go` | **New.** Migration tests. |
| `docs/dev/ARCHITECTURE.md` | Remove all `profiles/base/` references. Update load path descriptions. |

---

## Commit Plan

1. **Phase 1:** Baked-in defaults YAML + scaffold generation + `OS` field + `ConfigPath()` redirect. `make check` passes.
2. **Phase 2:** Two load paths (`LoadDefaultsConfig`, `LoadProfileConfig`), `MergeConfigs`, unknown field validation, `GetEffectiveConfig` update.
3. **Phase 3:** `EnsureSetup` creates `defaults/` scaffold. `CheckDefaultsDir()` guards upgrade path (gated on `setup_complete`).
4. **Phase 4:** CLI wiring — `config set` scaffold write, `resolveBackend()` reads from config, remove `profile` key, update help text.
5. **Phase 5:** Remove `SeedResources()` writes to `profiles/base/`.
6. **Phase 6:** Tests + `ARCHITECTURE.md` update.
