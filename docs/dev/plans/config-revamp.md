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

# Preferred Linux container backend. Valid values: docker, podman.
# Both work on Linux and macOS. Ignored for vm/vm-enhanced (uses containerd)
# and os=mac (uses Seatbelt or Tart). CLI --backend overrides.
container_backend: docker

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
commenting out all uncommented, non-blank lines:

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

### Step 2.3 — `config/profile.go`: `LoadProfileConfig()` — profile load path

Update profile loading so it merges over baked-in defaults only — never over `defaults/`:

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

    override, err := config.ParseConfigYAML(data, profileConfigPath)
    if err != nil {
        return nil, err
    }

    return config.MergeConfigs(base, override), nil
}
```

Update `MergedConfig` and its consumers in `profile.go` to use this path. Remove the old
`base → profile` chain (which used `LoadConfig()` as the base).

### Step 2.4 — Unknown field validation

Add validation to `parseConfigYAML()` — after parsing, check that every top-level key in
the YAML document is a recognized field. Return a descriptive error if not:

```go
// knownTopLevelKeys is the set of valid top-level keys in a profile/defaults config.
var knownTopLevelKeys = map[string]bool{
    "os": true, "agent": true, "model": true, "container_backend": true,
    "isolation": true, "tart": true, "network": true, "agent_files": true,
    "mounts": true, "ports": true, "resources": true, "agent_args": true,
    "env": true, "auto_commit_interval": true, "cap_add": true,
    "devices": true, "setup": true,
    // profile-only (valid in profiles, no-op in defaults — accepted, not errored)
    "workdir": true, "directories": true,
}

// After parsing the root mapping, check for unknown keys:
var unknown []string
for i := 0; i < len(root.Content)-1; i += 2 {
    key := root.Content[i].Value
    if !knownTopLevelKeys[key] {
        unknown = append(unknown, key)
    }
}
if len(unknown) > 0 {
    sort.Strings(unknown)
    return nil, fmt.Errorf("%s: unknown config field(s): %s", source, strings.Join(unknown, ", "))
}
```

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
3. **Do not** write Dockerfile, entrypoint scripts, or tmux.conf to `defaults/` — these remain
   baked-in.

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

`EnsureSetup()` creates `defaults/` on fresh installs, but existing users upgrading from the
old layout will have `profiles/base/config.yaml` with their customizations and no `defaults/`.
Rather than silently auto-migrating (which could bring in now-invalid keys like `profile:`),
detect this case and error with clear instructions:

```go
// CheckDefaultsDir verifies that ~/.yoloai/defaults/ exists. If it doesn't,
// returns a descriptive error telling the user how to resolve it.
// Called early in EnsureSetupNonInteractive() before any config is read.
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
        "  Then remove any 'profile:' line from the copied file (that key no longer exists).\n"
    return config.NewConfigError(msg)
}
```

Call `CheckDefaultsDir()` from `EnsureSetupNonInteractive()` **before** `ensureDefaultsDir()`
so that upgrading users see the error rather than getting a blank scaffold that silently
discards their settings. The check only fires when `defaults/` is entirely absent — once
setup has run (or the user copies manually), it becomes a no-op.

`yoloai setup` runs `ensureDefaultsDir()` unconditionally, so it always creates `defaults/`
with the scaffold — that is the "Option 1" path above.

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

`runtime/docker/build.go`'s `SeedResources()` currently writes Dockerfile, entrypoint
scripts, and tmux.conf to `~/.yoloai/profiles/base/`. Under the new design, these are
baked-in and never written to disk. Confirm the Docker image build uses embedded bytes
(via `embeddedDockerfile`, `embeddedEntrypoint`, etc. in `resources.go`) rather than
reading from `profiles/base/`. If `SeedResources()` writes are still load-bearing for the
build, replace the file reads with the embedded vars. Then remove the `SeedResources()`
calls and function (or reduce it to a no-op stub if other callers remain).

**Checkpoint:** after this phase, `~/.yoloai/profiles/base/` should have no write path in
the new code. Existing `profiles/base/` directories on user machines are handled by migration
(Phase 3.2).

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
| `config/config.go` | Extract `parseConfigYAML()`. Add `LoadBakedInDefaults()`, `LoadDefaultsConfig()`, `MergeConfigs()`. Add `knownTopLevelKeys` set and unknown-field validation in `parseConfigYAML()`. Update `GetEffectiveConfig()`. |
| `config/profile.go` | Update `LoadProfileConfig()` / `MergedConfig` to use baked-in defaults as base instead of `LoadConfig()`. Remove old base→profile chain. |

### Phase 3

| File | Change |
|------|--------|
| `sandbox/manager.go` | Add `ensureDefaultsDir()`. Call from `EnsureSetupNonInteractive()`. Call `MigrateIfNeeded()` early. |
| `config/migration.go` | **New.** `CheckDefaultsDir()` — errors with migration instructions if `defaults/` absent. |

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
| `runtime/docker/build.go` | Confirm image build uses embedded bytes. Remove `SeedResources()` writes to `profiles/base/`. |

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
3. **Phase 3:** `EnsureSetup` creates `defaults/` scaffold. `MigrateIfNeeded` from `profiles/base/`.
4. **Phase 4:** CLI wiring — `config set` scaffold write, `resolveBackend()` reads from config, remove `profile` key, update help text.
5. **Phase 5:** Remove `SeedResources()` writes to `profiles/base/`.
6. **Phase 6:** Tests + `ARCHITECTURE.md` update.
