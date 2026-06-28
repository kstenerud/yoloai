# Breaking Changes

Tracks breaking changes made during beta. Each entry should be included in release notes for the version that introduces it.

## Unreleased

### Credential brokering is the default on supported backends (the agent's API key no longer enters the sandbox)

When an agent's API key is brokerable (currently Claude's `ANTHROPIC_API_KEY`),
the backend can host a host-side injector (Linux docker/podman), the key is
present, and networking is open, yoloai now **brokers the key by default**: it
runs a small host-side reverse proxy and points the agent at it
(`ANTHROPIC_BASE_URL` + a placeholder `ANTHROPIC_AUTH_TOKEN`), so the **real key
is held host-side and never enters the container** (D105/D106). Previously the
key was written into the container environment, and brokering was opt-in
(`--broker`).

**What breaks:** anything *inside* the sandbox that read the raw key from the
environment — e.g. a shell script or tool calling the provider API directly with
`$ANTHROPIC_API_KEY`. Under brokering that variable is absent; in-container code
must call the agent's configured `ANTHROPIC_BASE_URL` (which carries the
placeholder token and is swapped to the real key host-side) instead of holding
the key itself.

**Opt out:** pass **`--no-broker`** on `yoloai new` / `yoloai run` to deliver the
key directly as before. The posture is persisted and sticky across restart.
`--broker` still forces brokering on (and now errors if the backend can't host an
injector, rather than silently doing nothing).

**Scope / not yet brokered (unchanged direct delivery, no action needed):**
non-brokerable agents; subscription/OAuth logins (no static key to broker);
backends other than Linux docker/podman; and **restricted networking**
(`--network-isolated` / `--network-none`) — the in-sandbox allowlist can't reach
the host-side injector yet, so auto-brokering is skipped there and an explicit
`--broker` with restricted networking is rejected. Composing brokering with an
egress allowlist is a later phase.

## v0.5.0

### Agent type and model move off the substrate record onto `agent.json` / `SandboxInfo` / `Sandbox.Agent()`

The agent type and model are no longer fields of the substrate record. They are
**inside-process config** — configuration of a process that runs *inside* the
sandbox — not facts about the sandbox container itself, so they are split out
(Q104) ahead of promoting the store to a public layer.

- **On disk:** `environment.json` no longer carries `agent` / `model`. A new
  sibling file **`agent.json`** (`{"version":1,"agent":...,"model":...}`) holds
  them. The substrate record advances to schema **v3**.
- **Public read-model:** `yoloai.Environment` (carried on
  `SandboxInfo.Environment`) loses `AgentType` / `Model`. They move **top-level
  onto `yoloai.SandboxInfo`** (`info.AgentType`, `info.Model`), next to
  `AgentStatus`, and are also reachable per-handle via
  **`sb.Agent().Type()` / `sb.Agent().Model()`**.

**Why:** promoting a substrate record that also describes a tenant process's
configuration would freeze that conflation into the public API. The substrate
record should carry constitutive/policy/provenance facts only; "what runs
inside" belongs to the orchestration layer.

**What breaks:** Go embedders reading `env.AgentType` / `env.Model` off the
`Environment` view, or parsing `agent` / `model` out of the `Environment` JSON;
anything reading `agent` / `model` directly from `environment.json` on disk.
Shell pipelines that extract agent or model from `--json` output must also update
their filters (see migration below).

**Migration:** `info.Environment.AgentType` → `info.AgentType` (or
`sb.Agent().Type()`); likewise for `Model`. Existing sandboxes need a one-time
**`yoloai system migrate`** — the data dir schema bump (realm v2 → v3) makes the
startup gate prompt for it. The migration relocates each sandbox's `agent` /
`model` into `agent.json` and stamps `environment.json` to v3; it is idempotent
and writes `agent.json` before rewriting `environment.json`, so an interrupted
run loses nothing. Until migrated, a sandbox's `environment.json` balks on load
with a "needs migration" error rather than being rewritten on read.

**CLI wire-format migration (`--json`):** `yoloai sandbox info --json` and
`yoloai sandbox list --json` now emit `agent` and `model` as **top-level keys**
on the `SandboxInfo` object, not nested under `environment`:

- `jq '.environment.agent'` → `jq '.agent'`
- `jq '.environment.model'` → `jq '.model'`
- `jq '.sandboxes[].environment.agent'` → `jq '.sandboxes[].agent'`
- `jq '.sandboxes[].environment.model'` → `jq '.sandboxes[].model'`

MCP tool outputs (`sandbox_status`, `sandbox_list`, `sandbox_wait`) are **not
affected** — they already build an explicit top-level `agent` key and did not
expose the `environment` nesting.

### Network policy moves off the substrate record onto `netpolicy.json` / `SandboxInfo` (D90)

The network policy fields (`network_mode`, `network_allow`) are no longer fields
of the substrate record. They are **network-layer config** — policy describing
what the sandbox process is permitted to reach — not facts about the sandbox
container itself, so they are split out (D90) into a dedicated record.

- **On disk:** `environment.json` no longer carries `network_mode` /
  `network_allow`. A new sibling file **`netpolicy.json`**
  (`{"version":1,"network_mode":...,"network_allow":[...]}`) holds them. The
  substrate record remains at schema **v3** — this relocation is part of the same
  v3 migration step as the Q104 agent/model split above.
- **Public read-model:** `yoloai.Environment` loses `NetworkMode` / `NetworkAllow`.
  They move **top-level onto `yoloai.SandboxInfo`** (`info.NetworkMode`,
  `info.NetworkAllow`), next to `AgentType` / `Model`.

**Why:** the same principle as Q104 — the substrate record should carry
constitutive facts about the sandbox container, not policy belonging to another
layer. Network policy is orchestration-layer config (D90).

**What breaks:** Go embedders reading `env.NetworkMode` / `env.NetworkAllow` off
the `Environment` view, or parsing those keys out of `environment.json` on disk.
Shell pipelines reading `network_mode` / `network_allow` from `--json` output
must update their filters.

**Migration:** `info.Environment.NetworkMode` → `info.NetworkMode`; likewise for
`NetworkAllow`. The same **`yoloai system migrate`** that handles Q104 also
writes `netpolicy.json` for each sandbox. The two sibling files (`agent.json`,
`netpolicy.json`) are written before `environment.json` is rewritten, so a crash
mid-migration is safe to resume.

**CLI wire-format migration (`--json`):** `yoloai sandbox info --json` now emits
`network_mode` and `network_allow` as **top-level keys** on the `SandboxInfo`
object, not nested under `environment`:

- `jq '.environment.network_mode'` → `jq '.network_mode'`
- `jq '.environment.network_allow'` → `jq '.network_allow'`
- `jq '.sandboxes[].environment.network_mode'` → `jq '.sandboxes[].network_mode'`

### `yoloai diff --json` gains `files`, `additions`, and `deletions` keys (additive)

`yoloai diff <name> --json` now includes three additional keys alongside `diff`:

```json
{
  "diff": "...",
  "files":     [{"path": "...", "change": "modified", "additions": 3, "deletions": 1}, ...],
  "additions": 4,
  "deletions": 1
}
```

This is **additive** — `jq '.diff'` continues to work unchanged. Strict JSON-schema
consumers that reject unknown keys must add `files`, `additions`, and `deletions`
to their accepted set.

### `Environment` exposes one ordered `Dirs` list instead of `Workdir` + `Directories`

The public read-model `yoloai.Environment` (carried on `SandboxInfo.Environment`)
replaced its singular `Workdir WorkdirInfo` field and separate `Directories
[]DirInfo` field with a single ordered **`Dirs []DirInfo`** where element 0 is
the workdir. The `WorkdirInfo` type is removed (one `DirInfo` type now, with an
added `InceptionSHA`). Access the workdir via the new `Workdir()` accessor, aux
dirs via `AuxDirs()`, and the diff/apply set via `TrackedDirs()`. The JSON shape
changes correspondingly: `{"workdir": {...}, "directories": [...]}` →
`{"dirs": [{...}, ...]}`.

**Why:** the singular workdir field forced every present and future multi-dir
feature to special-case "the workdir vs the rest". One ordered list removes that
seam — the groundwork for diff/apply across multiple tracked directories (D81).
Done now, in beta, because reshaping a load-bearing field is far cheaper before
the API stabilizes than after.

**What breaks:** Go embedders reading `env.Workdir.HostPath` / ranging
`env.Directories`, and any consumer parsing the `Environment` JSON.

**Migration:** `env.Workdir.X` → `env.Workdir().X`; `env.Directories` →
`env.AuxDirs()`. In JSON, read `dirs[0]` for the workdir and `dirs[1:]` for aux
dirs. On-disk sandbox metadata (`environment.json`) migrates automatically on
first read (schema v1 → v2); no user action is needed.

### macOS default isolation becomes `vm` (Apple `container`) when it is installed

When the Apple `container` CLI is installed (macOS 26+ on Apple Silicon), a
plain `yoloai new` on macOS now defaults to **`vm` isolation** via the new
`apple` backend — each sandbox runs as a Linux OCI container in its own
lightweight VM — instead of a shared-kernel container on Docker/Podman/OrbStack.

**Why:** Apple's per-container VMs boot in well under a second, so the stronger
isolation is effectively free on macOS; making it the default gives a safer
out-of-the-box posture. The platform rule is "VM-default only where VMs are
cheap to start" — on Linux, VM isolation stays opt-in (`--isolation vm`) because
containerd/Kata is heavy to start.

**What changes:** on a macOS host with `container` installed and no explicit
backend/isolation preference, sandboxes get a VM boundary (host kernel not
shared). Behavior is otherwise the same — `:copy`/`:overlay` diff-apply,
`--network-isolated`, restart, and `exec`-based `attach` all work. There is no
suspend/resume and no VS Code "Attach to Running Container" on this backend.

**Opt out / pin the old behavior:**

- `--isolation container` (or `isolation: container` in config) keeps a
  shared-kernel container on Docker/Podman/OrbStack.
- An explicit `--backend` / `container_backend` (e.g. `orbstack`,
  `docker-desktop`, `podman`) wins over the apple default.
- Hosts without `container` installed are unaffected — the default stays a
  container backend.

The setup wizard's environment step now offers these as named presets
(`apple` recommended, plus `orbstack`/`docker-desktop`/`podman`/`tart`/`seatbelt`).

### `${VAR}` interpolation in config/profile values resolves only a fixed allowlist

`${VAR}` references in config and profile values (and in `.yoloai.yaml`, CLI dir
specs, mount specs, prompt-file paths, and `agent_files`/`model_aliases`)
previously resolved against the **entire** edge-resolved host environment — any
process env var, including secrets like `${ANTHROPIC_API_KEY}` or
`${DATABASE_URL}`, would substitute. Interpolation now resolves only a fixed
allowlist: **`HOME`, `USER`, `LANG`, `TZ`, and any `LC_*`** (locale) var. Any
other `${VAR}` reference now fails with `environment variable "VAR" is not set`.

**Why:** config interpolation was the last arbitrary-key reader of the host-env
snapshot. Restricting it to locale/home vars closes that path so a config value
can no longer pull an arbitrary host secret into a sandbox or command line
(DEV §12). Credentials still reach agents the supported way — injected as files
under `/run/secrets`, declared via the agent's `APIKeyEnvVars`/`AuthHintEnvVars`.

**What breaks:** a config/profile that interpolated a non-allowlisted var (e.g.
`workdir.path: ${MY_PROJECTS}/app` or `env: { TOKEN: ${MY_TOKEN} }`) now errors
at load instead of substituting.

**Migration:** inline the literal value, or move the value into the config key
directly (for `env:`, set the value literally — the credential injection path is
unchanged). There is intentionally no opt-in to widen the allowlist yet; if you
relied on interpolating another var, please open an issue describing the use case.

### `IsolationAvailability` gains macOS Apple-`container` signals (Go embedding surface)

The public `yoloai.IsolationAvailability` signature changed from
`(isolation, targetOS, hostOS string)` to
`(isolation, targetOS, hostOS string, hostMacOSMajor int, containerInstalled bool)`.

**Why:** macOS `--isolation vm` now routes to the new `apple` backend, so the
availability message must distinguish "Apple `container` not installed" from
"macOS too old to run it". The two new parameters carry those host facts.

**Migration:** pass the host's macOS major version and whether the `container`
CLI is installed — `yoloai.AppleVMHostSignals()` returns exactly that pair:

```go
major, installed := yoloai.AppleVMHostSignals()
avail, reason, help := yoloai.IsolationAvailability(iso, targetOS, hostOS, major, installed)
```

### `TartBaseAdmin.AvailableRuntimes` / `PlanBase` take a `context.Context`

Both methods on the public `yoloai.TartBaseAdmin` gained a leading
`ctx context.Context` parameter — `AvailableRuntimes(ctx)` and
`PlanBase(ctx, specs)` — threading cancellation/timeout through the `tart`
subprocesses they invoke.

**Migration:** `a.AvailableRuntimes()` → `a.AvailableRuntimes(ctx)`;
`a.PlanBase(specs)` → `a.PlanBase(ctx, specs)`.

## v0.4.0

### Default Tart base image now tracks the host's macOS instead of being pinned to Sequoia

The Tart backend's default base image was hardcoded to
`ghcr.io/cirruslabs/macos-sequoia-base:latest`. It now resolves to the Cirrus
base whose macOS major **matches the host** (read via `sw_vers`), so the guest
is new enough to run the host's Xcode without waiting for a yoloai release.
Hosts on a macOS major yoloai doesn't yet map fall back to the newest known
base (currently Tahoe).

**What breaks:** on first `new`/`setup` after upgrading, a host on a macOS other
than Sequoia resolves to a different base and **re-pulls + reprovisions** the
`yoloai-base` VM (a one-time ~30 GB download). A Sequoia host is unaffected. The
old Sequoia base image is left on disk (see the new prune selector below), not
auto-deleted.

**Migration / opt-out:**

- To pin a specific macOS (stay on the old base, or jump to a brand-new one the
  day Cirrus publishes it), set `tart.image` in config — it always wins over the
  host match:
  ```yaml
  tart:
    image: ghcr.io/cirruslabs/macos-sequoia-base:latest
  ```
- After a host-OS upgrade, reclaim the superseded base with
  `yoloai system prune --stale-bases` (or follow the hint in `yoloai doctor`).

### `yoloai system prune` gains a `--stale-bases` selector

A host-OS change leaves the previous Tart base image on disk; it is matched by
neither the orphan sweep nor `--images` (which only targets the *current* base).
The new `--stale-bases` selector removes superseded base images (it never
touches the current base, so it forces no rebuild). This is additive — no
existing invocation changes — and parallels the existing `--images`/`--trash`
selectors. `yoloai doctor` reports superseded bases and the reclaim command.

### Scope-widening confirmations replaced by selector flags (`--yes` no longer widens scope)

The CLI never prompts to widen the destructive scope, and `--yes` no longer
authorizes any collateral danger. Opting into a consequence beyond the verb you
invoked is now an explicit, consequence-named selector flag; absent the flag the
command **hard-refuses with a typed error in every mode** (interactive, `--json`,
or piped) rather than prompting.

**What breaks:**

| Command | Before | After |
|---|---|---|
| `new` | dirty workdir → interactive confirm; `--yes` auto-proceeds | dirty workdir → **refuses** unless `--allow-dirty`; `--yes` removed |
| `destroy` | active/unapplied work (incl. a running agent) → confirm; `--yes` auto-proceeds | unapplied changes → **refuses** unless `--abandon-unapplied`; a running agent alone no longer blocks; `--yes` removed |
| `system prune` | trash emptied when `--yes` set (and never under `--json`) | trash emptied only with `--trash` (a selector, parallel to `--images`); `--yes` now only suppresses the reclaim prompt |

`--yes` survives only on commands whose prompt confirms *the verb you invoked*
(`apply`, `system prune`'s reclaim, `profile delete`, `system tart remove`).

**Migration:**

- `yoloai new … ` on a dirty repo → add `--allow-dirty`.
- `yoloai destroy … --yes` → `yoloai destroy … --abandon-unapplied` (only if the
  target has unapplied changes; a running-but-clean agent no longer needs the
  flag, so otherwise just drop `--yes`).
- `yoloai system prune --yes` that relied on emptying the trash → add `--trash`.
- Scripts can detect the refusals via `errors.As` on `*yoloai.DirtyWorkdirError`
  (new) and the active-work error (destroy).

### CLI `--json` output is always a top-level object

The `--json` output now follows a fixed structural convention (see
`docs/contributors/standards/cli.md`): **every command emits a top-level JSON
object, never a bare array.** List commands wrap their items in a semantically
named array field, and an empty array is always `[]`, never `null`.

**What breaks** — five commands that previously emitted a bare top-level array
now emit an envelope object:

| Command | Before | After |
|---|---|---|
| `system backends` | `[…]` | `{"backends": […]}` |
| `system agents` | `[…]` (or `null` when empty) | `{"agents": […]}` |
| `x` / `extensions list` | `[…]` | `{"extensions": […]}` |
| `stop` | `[{name, action}]` | `{"stopped": [{name, action}]}` |
| `destroy` | `[{name, action}]` | `{"destroyed": [{name, action}]}` |

Also fixed: `system agents` and `extensions list` emitted `null` for an empty
list; they now emit `[]`.

Alongside the envelope change, the same convention pass also:

- **`sandbox unlock`**: renamed the identifier key `sandbox` → `name` (now uniform
  with every other command). `jq '.sandbox'` → `jq '.name'`.
- **Empty arrays never `null`**: `sandbox allowed`'s `domains`, `system backend <name>`'s
  `platforms`/`tradeoffs`, and `profile info`/`--diff`'s `chain` now serialize as
  `[]` (not `null`) when empty.
- **`clone --no-start`** now includes `"action": "cloned"` (additive — its started
  sibling already carried `"action": "started"`).
- **`profile list`** gained `--json` support, emitting `{"profiles": [{name,
  has_dockerfile, agent}]}` (previously it ignored `--json` and printed a table).

**Migration (consumers):** update `jq` filters from `.[]` to `.<key>[]` for these
commands — e.g. `system backends --json | jq '.backends[]'`,
`destroy --json | jq '.destroyed[]'`. Commands that were already objects
(`sandbox list`, `system disk`, `diff --log`, `system check`, all single-record
and action commands) keep their shape. Rationale: a bare top-level array can carry
neither a top-level error nor future metadata; a uniform object shape lets every
command grow without breaking parsers. See finding DF17.

### Creation is dormant; `CreateSandbox` / `Sandbox.Clone` return a live `*Sandbox`

`CreateSandbox` and the new `Sandbox.Clone` now *provision only* — they no longer
launch the container. Each returns a live but unstarted `*Sandbox` handle (was
`(string, error)` for create, `error` for clone), so embedders no longer make a second
`Sandbox(name)` lookup. Cloning moved off the `Client` root onto the source sandbox
handle — `srcSb.Clone(ctx, dest, SandboxCloneOptions{Overwrite: …})` — since the source
is a pre-existing noun; `SandboxCloneOptions` keeps only `Overwrite` (Source is the
receiver, Dest is the argument). Launch is an explicit, separate step:

```go
sb, err := client.CreateSandbox(ctx, yoloai.SandboxCreateOptions{ … })
if err != nil { … }
if _, err := sb.Start(ctx, yoloai.SandboxStartOptions{}); err != nil { … }

// clone:
srcSb, err := client.Sandbox("source")
if err != nil { … }
clone, err := srcSb.Clone(ctx, "dest", yoloai.SandboxCloneOptions{})
if err != nil { … }
if _, err := clone.Start(ctx, yoloai.SandboxStartOptions{}); err != nil { … }
```

`Sandbox.Start` already owned first-launch (its `StatusRemoved` path does the
container launch + workdir-baseline setup), so the create-time prompt is delivered on
that explicit `Start` exactly as before. The old `--no-start` CLI flag is honored by
skipping the `Start` call.

The waiting behavior that `Client.Run(Wait: true)` provided is now `Sandbox.Wait`:

```go
info, err := sb.Wait(ctx, yoloai.SandboxWaitOptions{
    For:     yoloai.WaitForExit, // or WaitForIdle
    Timeout: 0,                  // 0 = wait indefinitely
})
```

`Wait` polls until the requested condition is met (`WaitForExit` settles on any
terminal status; `WaitForIdle` also settles on `StatusIdle`). On timeout it returns
the last-observed `*SandboxInfo` plus `ErrWaitTimeout` (which wraps
`context.DeadlineExceeded`); on caller cancellation it returns `ctx.Err()`.

### Public read-model field renames (Go embedding surface)

A naming-audit pass renamed read-model fields whose old names leaned on a comment to
supply a qualifier the name itself should have carried. These are Go-surface renames
only; field semantics and zero values are unchanged.

- `SandboxInfo.HasChanges string` → `Changes ChangeState`. The old string-typed field
  read like a boolean but carried three values; it is now the typed tri-state
  `ChangeState` (`ChangesPresent` / `ChangesAbsent` / `ChangesUnknown`). The `--json`
  wire tag stays `"has_changes"`, so JSON output is byte-stable.
- `SystemInfo.GlobalConfig` → `GlobalConfigPath`; `SystemInfo.DefaultsConfig` →
  `DefaultsConfigPath` (both are filesystem paths).
- `DiskUsage.Sandboxes` → `SandboxesBytes` (a byte count).
- `TartBaseInfo.Size` → `SizeBytes` (a byte count).
- `BackendReport.Backend string` → `Type BackendType` (a backend kind, now typed).
- `PruneItem.Bytes` → `BytesReclaimed` (the space freed by removing the item).
- `TrashedSandbox.From` → `OriginalPath`; `TrashedSandbox.Dest` → `TrashPath`.

**Migration (Go embedders):** rename the field accessors at call sites; for
`SandboxInfo.Changes`, compare against the `ChangeState` constants instead of the bare
strings `"yes"`/`"no"`/`"-"`; for `BackendReport.Type`, the value is now `BackendType`
(convert with `string(r.Type)` where a plain string is needed).

### `BuildImageOptions` backend selection collapsed to one required field

`BuildImageOptions` previously carried a `BackendType` field *and* a mutually-exclusive
`AllBackends bool`, with an empty `BackendType` implicitly meaning "the default
backend". The boolean is removed and `BackendType` is now required, selecting the
backend(s) via a single value:

- a specific backend (`BackendDocker`, …) builds for that backend,
- the new reserved selector `BackendsAll` builds for every registered backend,
- the new reserved selector `BackendDefault` builds for the config-resolved
  container backend.

An empty `BackendType` is now rejected with a usage error — there is no implicit
default. `BackendsAll` (`"all"`) and `BackendDefault` (`"default"`) are reserved names
that can never be real backends; they are only meaningful for
`BuildImageOptions.BackendType`.

**Migration (Go embedders):** `AllBackends: true` → `BackendType: BackendsAll`; a
previously-empty `BackendType` (the old implicit default) → `BackendType: BackendDefault`.

### Launch-prefix legacy path removed; library data dir migrates to schema v2

The agent launch command is wrapped with a backend-specific prefix (Tart prepends a
`PATH=…`; Seatbelt sources `~/.swift-wrapper.sh`; container backends use no wrap). That
prefix is now stored once at creation in each sandbox's `runtime-config.json`
(`agent_launch_prefix`) as the single source of truth. The old per-backend Python fallback
(`prepare_launch_command` in `sandbox-setup.py`) that reconstructed the prefix at runtime for
sandboxes lacking the stored field is **removed**, along with the `use_launch_prefix` guard.

**Previous behavior:** a sandbox whose `runtime-config.json` had no `agent_launch_prefix`
relaunched its agent via the Python `prepare_launch_command` recomputation path.

**New behavior:** the stored `agent_launch_prefix` is always used. The launch prefix is a
per-backend constant fully derivable host-side, so the library bumps `LibrarySchemaVersion`
to `2` and the v1→v2 step of `yoloai system migrate` (re)writes `agent_launch_prefix` for
every sandbox from its stored backend type. This is idempotent — sandboxes that already carry
the field get the identical deterministic value, so upgraders who created sandboxes on an
earlier build of this line are unaffected. Container-backend sandboxes (docker/podman/
containerd) resolve to an empty prefix (a no-op wrap); in practice only Tart and Seatbelt
sandboxes change.

**Migration:** run `yoloai system migrate` once after upgrading (the startup gate already
requires this for any out-of-date data dir — see the data-directory bifurcation entry). The
migration is idempotent. A pre-migration Tart/Seatbelt sandbox driven by the new binary
*without* migrating would fail to relaunch its agent (the fallback is gone); migrating fixes
it with no re-creation.

### `--force` flags renamed for their consequence (`new`, `clone`, `files`, `system build`)

**Previous behavior:** Five sites overloaded `--force`, each with a different effect:

- `yoloai new --force` — replace a sandbox even when it holds unapplied changes or a running agent (the unsafe sibling of `--replace`).
- `yoloai clone --force` — overwrite an existing destination sandbox.
- `yoloai files put/get --force` — overwrite existing files.
- `yoloai system build --force` (and `--all`) — rebuild an image that is already up to date.

**New behavior:** there is no `--force` flag anywhere in the CLI. Each is replaced by a flag named for its consequence:

- `yoloai new --abandon-unapplied` — implies `--replace`. `--replace` is unchanged: still the safe option that destroys a *clean* same-named sandbox first and aborts when one holds unapplied work.
- `yoloai clone --overwrite`
- `yoloai files put/get --overwrite`
- `yoloai system build --rebuild`

The `<dir>:force` directory-spec suffix (bypass the dangerous-mount-path guard) is **unchanged** — it is a path-mode suffix, not a `--force` flag.

**Rationale:** one `--force` spelling hid four different consequences — discarding unreviewed work, clobbering a sandbox, clobbering files, redoing a build — so the flag's effect was unreadable at the call site. This mirrors the library API, which already replaced its generic `Force` option with consequence-named options (`AbandonUnappliedWork`, `Overwrite`, `Rebuild`). Naming a destructive flag after its effect makes the danger legible where it is invoked.

**Migration:** in scripts, replace `--force` per command — `new` → `--abandon-unapplied`, `clone` → `--overwrite`, `files put`/`files get` → `--overwrite`, `system build` → `--rebuild`. The old `--force` is removed with no deprecation alias.

## v0.3.0

### Public Go embedding surface reshaped (0.x layer-1)

The root `yoloai` package was reshaped so external embedders drive yoloAI entirely
through it (never importing `internal/*`): per-sandbox operations moved onto
resource-bound handles, creation and backend-selection became explicit, and every
Options/result/error is now a public `yoloai.*` type. **The overwhelming majority of
this surface is new in this release** — the handles, the `<Noun><Verb>Options`
family (`SandboxCreateOptions`, `WorkdirDiffOptions`, …), the
typed errors, the kind enums, and the `System()` / `Sandbox()` / `Agent()` /
`Workdir()` accessors all describe new API with no prior name to migrate from. CLI
behavior is unchanged except for the wire-format renames called out at the end.

**What actually breaks relative to the last release.** Only the handful of names
that shipped on the prior stable surface change; this is the entire Go migration:

- `Options` → `ClientCreateOptions` (its `Backend` field is now `BackendType`,
  typed `yoloai.BackendType`).
- `Client.Run` / `RunOptions` were removed outright (not renamed). Creation no
  longer launches: `CreateSandbox` provisions a *dormant* `*Sandbox`, which the
  caller starts explicitly with `Sandbox.Start` and (optionally) blocks on with
  the new `Sandbox.Wait`. See "Creation is dormant" below.
- `ApplyOptions` → `WorkdirApplyOptions`, now reached via
  `client.Sandbox(name).Workdir().Apply(…)`.
- `NewWithOptions(ctx, Options)` → `NewClient(ctx, ClientCreateOptions)`.
- `New(ctx)` (the no-argument constructor) was removed — use
  `NewClient(ctx, ClientCreateOptions{})`.
- The per-`name` `Client` methods that shipped before — `Inspect`, `Stop`,
  `Destroy`, `Diff`, `Apply`, `ApplyWithOptions` — are no longer flat on `Client`.
  Call them on the handle returned by `client.Sandbox(name)` (`.Inspect(ctx)`,
  `.Stop(ctx)`, `.Destroy(ctx, …)`), and route diff/apply through
  `.Sandbox(name).Workdir()`. `Close` remains on `Client`.
- `List` → `ListSandboxes`. The sandbox verbs on the root `Client` now name
  their noun: `ListSandboxes` and `CreateSandbox` (the latter introduced in this
  same reshape). `Client` is a multi-noun root — the bare verbs didn't say what
  they acted on. Cloning is a per-sandbox operation off the source handle,
  `Sandbox.Clone` (see "Creation is dormant" below).

Field semantics and zero values are otherwise unchanged — only the names and
receivers move.

**Migration (Go embedders):** rename the four type/constructor names at call sites;
insert `.Sandbox(name)` and drop the `name` argument from per-sandbox calls; route
diff/apply/export/commits/tags through `.Workdir()`; replace `New(ctx)` with
`NewClient(ctx, ClientCreateOptions{})`.

**Orientation — the new shape (all new API, no prior name to migrate from).**
`Client.Sandbox(name)` returns `(*Sandbox, error)` and rejects a missing sandbox
with `ErrSandboxNotFound` at construction; its `.Workdir()` / `.Network()` /
`.Agent()` accessors are pure namespace expansion (no IO, no error). Admin/fleet
operations are reached via `Client.System()`. A `Client` built without
`ClientCreateOptions.BackendType` serves every backend-free operation (admin,
per-sandbox reads) without ever opening a runtime; a backend-bound call on such a
`Client` returns the typed sentinel `ErrBackendRequired`. Diff/apply/export and the
commit/tag/uncommitted reads live on `Workdir()`, each mode-agnostic;
`WorkdirApplyOptions.Mode` is required (`ApplyModeCommits` replays the commit series,
`ApplyModeNoCommit` lands a net diff). Errors are typed public shapes
(`*ActiveWorkError`, `*DirtyWorkdirError`, `*UsageError`, …) — use `errors.As`. The
kind enums read as `AgentType` / `BackendType` (value constants like `BackendDocker`
unchanged). Interactive `Exec` / `Attach` take an `IOStreams` of opaque byte streams
— the library never touches a stream FD, sets raw mode, or installs signal handlers;
initial geometry comes from `Rows`/`Cols` and live resizes arrive on
`IOStreams.Resize`. Backend selection is explicit via
`yoloai.SelectBackend(ctx, preferred, isolation, targetOS, env)` /
`SelectContainerBackend(ctx, preferred, env)`, both taking a host-env snapshot
rather than reading the process environment.

**Wire-format / CLI breakage (genuine vs the last release).**

- `sandbox info` / `list` `--json` nest the creation-time settings under
  `"environment"` (was `"meta"`), aligning with the on-disk `environment.json`
  artifact they mirror.
- The `sandbox info` / `list` read-model is curated: pure-mechanism fields
  (`version`, `yoloai_version`, `image_ref`, `has_prompt`, `debug`, `userns_mode`,
  `archetype`, `vscode_tunnel`, `inception_sha`) are dropped from both `--json` and
  the human output (the `Image:` and `Version:` lines are gone). `network_mode` now
  marshals from the typed `yoloai.NetworkMode` enum but to the same JSON string, so
  network output is byte-stable.
- `profile info` / `--diff` `--json`: the `agent_files` object's inner keys now
  carry tags (`base_dir` / `files`) instead of the Go field names (`BaseDir` /
  `Files`).
- `yoloai apply`: `--squash` → `--no-commit` (JSON `method` `"squash"` →
  `"no-commit"`). See the `yoloai apply` entry below for the commits-only default and
  `--include-uncommitted`.

### Data directory bifurcated into `library/` (engine) + `cli/` (app); one-time migration via `yoloai system migrate`

The CLI's `~/.yoloai/` is now split into two namespaces under the same top dir:

- `~/.yoloai/library/` holds everything the embeddable engine owns — `sandboxes/`,
  `profiles/`, `cache/`, `trash/`, `defaults/`, `config.yaml`, the tart/docker
  base-image locks + metadata, `cni/`, and `vscode-cli/`.
- `~/.yoloai/cli/` holds CLI-only application state — `extensions/` and the new
  `state.yaml` (first-run-tip bookkeeping).

**Why.** yoloAI is library-first: the engine owns everything under whatever
`DataDir` it is handed and never reaches above it, so the CLI now points the
library at `~/.yoloai/library/` and keeps its own app state in `~/.yoloai/cli/`.
This is purely a CLI convention — **embedders that pass an explicit `--data-dir`
(or `Options.DataDir`) still get every engine directory directly under that path,
with no `/library` subdir.** Each namespace carries its own plain-text-integer
`.schema-version` stamp (`<DataDir>/.schema-version` for the library,
`~/.yoloai/cli/.schema-version` for the CLI), so future layout changes migrate
independently.

**Migration is explicit — run `yoloai system migrate` once after upgrading.**
yoloAI no longer migrates your data directory automatically. On startup a
read-only check inspects each namespace's version stamp and, if your `~/.yoloai/`
predates this layout (a flat `config.yaml` with no `library/` beside it, or an
out-of-date stamp), the binary **fails fast** and tells you to run
`yoloai system migrate` — it will not touch your data until you do. That one
command relocates the engine dirs into `library/` and `extensions/` into `cli/`
(in-place renames within one filesystem — atomic, no copying), then stamps both
namespaces. It is idempotent and safe to re-run if it is interrupted. A brand-new
install (absent or empty `~/.yoloai/`) is created fresh automatically on first
use; read-only commands like `yoloai version` and `yoloai help` work regardless
of the directory's state.

**`--data-dir`.** When supplied, the CLI roots the library at `DIR/library` and
its own state at `DIR/cli` (same split as the default). Embedders calling the Go
API directly are unaffected — they own the path they pass.

**`setup_complete` removed.** The legacy `~/.yoloai/state.yaml` `setup_complete`
flag is gone. "Has the setup wizard run?" is application ceremony, not library
state, so the library no longer tracks it — `EnsureSetup` runs idempotently
inside `Create`, and the CLI's one-time onboarding tip is now keyed off
`~/.yoloai/cli/state.yaml` (`first_run_tip_shown`). The migration carries a legacy
`setup_complete: true` forward as `first_run_tip_shown: true` so upgraders don't
see the tip resurface.

**`tmux_conf` default.** Now defaults to `default+host` (was empty). The library
"just works" via opinionated declarative defaults rather than depending on a
completed setup ceremony to populate config.

### `yoloai system doctor` moves to `yoloai doctor`

The host health-check command is promoted to a top-level verb as part of the
repair/cleanup surface (see `docs/contributors/archive/plans/system-repair-cleanup.md`). It now
reports reclaimable cruft and sandboxes holding unreviewed work in addition to
backend capability status, and delegates remediation to `yoloai system prune`
and `yoloai destroy`.

**Previous behavior:** `yoloai system doctor`.

**New behavior:** `yoloai doctor` (same flags: `--backend`, `--isolation`,
`--json`). The `system doctor` subcommand is removed.

**Migration:** replace `yoloai system doctor` with `yoloai doctor`.

### `yoloai sandbox <name> allowed --json` carries per-domain provenance

**Previous behavior:** `yoloai sandbox <name> allowed --json` emitted a flat `domains` array of strings:

```json
{ "name": "mybox", "network_mode": "isolated", "domains": ["api.anthropic.com", "example.com"] }
```

The same went for the `domains_removed` field of `yoloai sandbox <name> deny --json` and the Go API's `meta.NetworkAllow []string`.

**New behavior:** Each domain is now an object with a `source` field that identifies why it's on the list. The Go API exposes the same shape as `[]yoloai.AllowedDomain`.

```json
{
  "name": "mybox",
  "network_mode": "isolated",
  "domains": [
    {"domain": "api.anthropic.com", "source": "agent-requirement"},
    {"domain": "example.com",       "source": "user"}
  ]
}
```

The two source values:

- `"agent-requirement"` — the bound agent's `agent.Definition.NetworkAllowlist` requires this domain (e.g. `api.anthropic.com` for Claude). Removing it will break the agent itself.
- `"user"` — added by the user via `--network-allow` at create time or `yoloai sandbox <name> allow` at runtime.

Human-readable output also gains an `" (agent requirement)"` annotation next to each agent-required entry, and `yoloai sandbox <name> deny` now prints a warning when the removal hits an agent-required domain. The library does not block the removal (that's a UI policy decision).

**Migration:**

- Shell pipelines that did `yoloai sandbox <name> allowed --json | jq '.domains[]'` and expected raw strings should switch to `jq '.domains[].domain'`.
- Embedders using `yoloai.Client.Sandbox(name).Network().Allowed(ctx)` get `[]yoloai.AllowedDomain`; the `Source` field is a `yoloai.DomainSource` enum (`AllowedFromAgentRequirement` / `AllowedFromUser`).
- The on-disk `environment.json` (`meta.NetworkAllow`) is unchanged — it still stores `[]string`. Provenance is recovered at read time, so existing sandboxes continue to work without migration.

**Rationale:** Q-V (`api_surface.go` Q-V resolution 2026-05-25). Flattening provenance at the API boundary was the same anti-pattern as the Q-Q CLI-UI leak: information the implementation can answer for was being thrown away. Two real use cases motivated the change — "don't silently nuke an agent-required domain" warnings and "show me my additions vs baked-in defaults" management UIs.

### Auxiliary `:copy` and `:overlay` are no longer supported

**Previous behavior:** Any directory passed via `-d` (auxiliary mount) could carry the same `:copy` and `:overlay` mode suffixes as the workdir, in which case the directory participated in `yoloai diff` / `yoloai apply` alongside the workdir. The workflow was all-or-nothing: a single multi-directory diff was emitted, apply ran per-directory in sequence, and the first failure halted the chain.

**New behavior:** `-d` arguments accept only `:rw` (live bind-mount) and the default `:ro` (read-only reference). Passing `-d <path>:copy` or `-d <path>:overlay` now errors with a `*UsageError` pointing at the alternatives. `yoloai diff` / `yoloai apply` operate on the workdir only.

**Migration:** Pick whichever fits each use case:

- The aux dir was effectively a second project you wanted to track changes in → make it the workdir of a separate sandbox.
- The aux dir was sibling code (monorepo cousin, docs in a separate repo, tools repo) where live edits are fine → mount as `:rw` for a live bind.
- The aux dir was reference material the agent should read but not edit → leave it default (`:ro`); behavior unchanged.
- If the aux dir was a parent that contained the real project → make the parent the workdir.

**Rationale:** Q-U (`docs/contributors/archive/plans/layering-refactor.md` W-L8b; `api_surface.go` Q-U resolution 2026-05-25). The multi-directory diff/apply implementation was real but the user-visible surface was barely used: no per-directory selection, no cross-directory conflict resolution, no `:overlay`-only-not-`:copy` filtering. The API complexity required to do this properly significantly exceeded what the implementation actually did. Removing the surface now while we're in beta keeps `yoloai diff` / `yoloai apply` simple — if a real use case emerges, it can be restored with an API informed by that need.

**Affected Go API (for embedders):** diff/apply operate on the workdir only, via `client.Sandbox(name).Workdir()` (see the layer-1 reshape entry above). There is no multi-directory patch surface; `Workdir().Apply` returns a single `*yoloai.ApplyResult` (a `nil` result is the no-op signal).

### First-run setup is non-interactive when triggered implicitly; `yoloai system setup` is the explicit wizard

**Previous behavior:** On the first `yoloai new` / `yoloai run` of a fresh install (when `setup_complete=false`), if stdin was a TTY the user was dropped into the interactive setup wizard before the sandbox could be created — three prompts (tmux config, default backend on macOS, default agent). When stdin wasn't a TTY, the same code path auto-configured silently with `tmux_conf=default+host`.

**New behavior:** Implicit first-run setup is **always non-interactive**: it writes `tmux_conf=default+host` and marks `setup_complete=true`, then proceeds. The interactive wizard is only run explicitly via `yoloai system setup` (which is also how a user re-runs setup to change their defaults).

**Rationale:** Q-F (`docs/contributors/archive/plans/layering-refactor.md` W-L8b) resolved that library entry points must not perform interactive IO — the CLI owns prompts. The previous behavior coupled `sandbox.Manager` to stdin/stdout and made first-run UX unpredictable depending on TTY state. The library exposes no setup verb at all: all onboarding policy (tmux classification, choice enumeration, prompt copy) lives in the CLI wizard (`internal/cli/system/setup.go`), which writes the collected answers via `Config().Set` and discovers the valid choices via `System().AgentTypes` / `System().BackendTypes`. Implicit first-run materializes the `defaults/` tree inside `EnsureSetup` and proceeds non-interactively.

**Migration:**
- If you relied on `yoloai new` prompting on first run, run `yoloai system setup` once after install (or before your first `yoloai new`). Subsequent runs are unaffected.
- CI / scripted installs already running on non-TTY stdin see no behavior change.
- Embedders calling `Client.RunSandbox` before configuring defaults still auto-get `default+host` — no code change needed.
- Embedders that want a wizard supply their own prompt UI and write the collected answers via `Config().Set`.

### `yoloai system prune` always operates across all backends; `--all` and `--backend` removed

**Previous behavior:** `yoloai system prune` accepted `--backend <name>` to prune only that backend and `--all` to prune across every available backend. Default was the configured default backend.

**New behavior:** The command always prunes across every backend that's currently available. Both `--all` and `--backend` flags have been removed; passing them now errors.

**Rationale:** Per-backend pruning had no real-world use case — orphan detection is keyed off the sandbox dir on the host, which is shared across all backends. Selecting one backend either matched the same orphan set (if that backend was the only one with state) or missed orphans on other backends (silently). Always-cross-backend matches user expectation and matches the `Client.System().Prune` library shape resolved under Q-L. Tracked in `docs/contributors/archive/plans/layering-refactor.md` W-L8b Q-L.

**Migration:**
- Drop `--all` and `--backend` from scripts and CI invocations.
- The behavior of bare `yoloai system prune` matches the old `--all` invocation.

### `yoloai system prune --cache` renamed to `--images`; plain prune now reclaims the no-rebuild cache

**Previous behavior:** Bare `yoloai system prune` only removed orphaned resources (containers/VMs with no sandbox dir, stale temp dirs, lock files) — it reclaimed no backend cache. `--cache` reclaimed the backend image cache, snapshots, volumes, and build cache in one destructive step that always forced a yoloai-base rebuild.

**New behavior:** The reclaim is split into two tiers by the prune invariant *plain prune must never force a rebuild*:
- Bare `yoloai system prune` now **also reclaims each backend's no-rebuild cache** (Docker/Podman build cache, retired volumes, dangling images). The base image is kept, so a subsequent `yoloai new` still runs without rebuilding.
- `--cache` is renamed **`--images`**, which additionally removes base/profile images and forces a rebuild on the next `new`.

`yoloai system disk` now reports two columns (`CACHE` = no-rebuild reclaim, `IMAGES` = rebuild-forcing) and `yoloai doctor` splits "reclaimable space" into the same two tiers. Prune also now reports bytes reclaimed (`freed_bytes` in `--json`).

**Rationale:** `--cache` was too generic to convey the rebuild cost, and the old bare prune left obvious reclaimable build cache on disk. The invariant gives users a safe default (`prune`, no rebuild) and an explicit destructive lever (`--images`, forces rebuild). On Docker's containerd image store, the build cache pins image layers, so the build cache must be pruned for `image rm` to actually free disk — bare prune now does this. See `docs/contributors/backend-idiosyncrasies.md`.

**Migration:**
- Replace `yoloai system prune --cache` with `yoloai system prune --images`.
- If you relied on bare `prune` *not* touching the build cache, note it now does (this only reclaims regenerable cache; no rebuild is forced).

### `yoloai system runtime` renamed to `yoloai system tart`

**Previous behavior:** Apple simulator runtime base images were managed via `yoloai system runtime add|list|remove`. The command name read as generic, but the surface is structurally Tart-only (and is the only CLI subtree that imports `runtime/tart` directly).

**New behavior:** The subtree is now `yoloai system tart` (Pattern B, mirroring `podman machine`). The old `yoloai system runtime ...` invocations continue to work as a hidden alias and print a deprecation warning to stderr. The alias will be removed in a future breaking-changes window.

**Rationale:** Backend-scoped commands should name the backend explicitly. The new name makes the Tart scope honest and reserves `yoloai system <backend>` as the convention for any future backend-specific surfaces. Tracked in `docs/contributors/archive/design/layering.md` D1 and W-L2 of `docs/contributors/archive/plans/layering-refactor.md`.

**Migration:**
- Replace `yoloai system runtime ...` with `yoloai system tart ...` in scripts, docs, and CI.
- Until the alias is removed, the old invocation still works but emits a warning.

### `yoloai apply` defaults to commits-only; `--no-wip` removed in favor of `--include-uncommitted`

**Previous behavior:** `yoloai apply` applied agent commits AND uncommitted edits as unstaged files in the same invocation. `--no-wip` opted out of applying the uncommitted edits.

**New behavior:** `yoloai apply` defaults to **committed changes only**. Uncommitted edits the agent left in the work copy are detected, reported as a hint, and NOT applied. To bring them across as unstaged modifications (the old default), pass `--include-uncommitted`. The `--no-wip` flag has been removed.

The flip applies uniformly across the apply surface:

- Default format-patch path: applies commits; prints `Note: sandbox has uncommitted changes …` when uncommitted edits are present and excluded.
- `--no-commit`: lands a single net diff only (`git diff baselineSHA HEAD`). With `--include-uncommitted`, the net diff includes uncommitted edits (`git diff baselineSHA`, after `git add -A`).
- `--patches`: writes `*.patch` for commits; writes `uncommitted.diff` only when `--include-uncommitted` is set.
- `--no-commit` and `--include-uncommitted` are no longer mutually exclusive — `--no-commit` controls patch shape, `--include-uncommitted` controls scope.
- `:overlay` sandboxes have no commit/uncommitted distinction; the flag has no effect there and is silently accepted (previously `--no-wip` errored on overlay).

**Rationale:** "Apply commits the agent made" is what users typically want; uncommitted edits are by definition unsettled work the agent didn't finalize. Defaulting to including them surprised users who weren't expecting the agent's scratch state in their tree. Making `--include-uncommitted` opt-in matches the project's `--X-to-enable-non-default-behavior` CLI convention ([`dev/standards/CLI.md`](contributors/standards/cli.md)) and surfaces the uncommitted state explicitly so users can choose.

**Migration:**
- Drop `--no-wip` (it was a no-op for the new behavior anyway).
- If you relied on `yoloai apply` bringing across uncommitted edits, add `--include-uncommitted` to the command.
- The Go library API `client.Sandbox(name).Workdir().Apply(ctx, WorkdirApplyOptions{Mode: ApplyModeCommits})` is now commits-only; add `IncludeUncommitted: true` for the old behavior.

### Cross-process JSON files gain `schema_version` field with mismatch-fails-loudly policy

**Previous behavior:** `runtime-config.json` and `agent-status.json` had no explicit version field. Drift between Go (writer/reader) and Python (reader/writer) could silently misinterpret fields.

**New behavior:** Both files carry `"schema_version": 1`. Mismatch between writer and reader (e.g., a newer yoloai writes a file an older yoloai reads) causes Go's `parseStatusJSON` to discard the file and Python's `read_config` / status-monitor.py to raise `RuntimeError` with a specific message naming the file and the version mismatch. Missing `schema_version` is tolerated as legacy (pre-W2) and follows the original parsing path; only an explicit non-matching value triggers a failure.

**Rationale:** W2 of the architecture remediation plan ([`dev/archive/plans/architecture-remediation.md`](contributors/archive/plans/architecture-remediation.md)). The cross-process boundary needs a tripwire so coordinated Go/Python changes are explicit, not silent. Hard-fail on mismatch trades the inconvenience of re-creating a sandbox for the safety of not misreading a structurally different file.

**Bump policy:**
- **Additive changes** (new optional field with sensible defaults on both sides) do NOT require a version bump.
- **Required-field changes, removals, renames, semantic changes** require bumping the constant in three places coordinated: `runtimeConfigSchemaVersion` in `sandbox/create.go`, `RUNTIME_CONFIG_SCHEMA_VERSION` in `sandbox-setup.py`, and the inline `runtime_config_schema_version` in `status-monitor.py` (or `agentStatusSchemaVersion` in `sandbox/inspect.go` + `AGENT_STATUS_SCHEMA_VERSION` in `sandbox-setup.py` + shell-hook literals in `agent/agent.go`).
- Recreating a sandbox is the workaround for users running across an incompatible upgrade.

**Migration:** none required when upgrading — newly-created sandboxes carry `schema_version: 1`; existing sandboxes (no field) continue to work. If a future version bumps the constant and you downgrade, the older binary will hard-fail; re-create the sandbox.

### `container-nestable` isolation mode removed; use `container-privileged` instead

**Previous behavior:** `--isolation container-nestable` created a container with a targeted capability grant (`CAP_NET_ADMIN`, `CAP_NET_RAW`, `CAP_SYS_ADMIN`, `seccomp=unconfined`, `cgroupns=host`) intended as a minimal Docker-in-Docker mode.

**New behavior:** The mode no longer exists. `--isolation container-privileged` is the supported mode for Docker-in-Docker and Compose workloads. It passes `--privileged`, which grants full access to `/proc/sys` and `/sys/fs/cgroup` — both required for an inner Docker daemon. The sandbox image's `/etc/docker/daemon.json` pre-configures `fuse-overlayfs` as the storage driver.

**Rationale:** The capability delta between `container-nestable` and `container-privileged` was too narrow to provide a meaningful security boundary. Both modes required `CAP_SYS_ADMIN` + `seccomp=unconfined` + `apparmor=unconfined`, which is sufficient for container escape. Maintaining a separate mode implied a safety tier that didn't exist in practice.

**Migration:** Replace `--isolation container-nestable` with `--isolation container-privileged`. Update any profile `config.yaml` files with `isolation: container-nestable` to `isolation: container-privileged`.

### `apply --force` flag removed; dirty trees are handled automatically

**Previous behavior:** `yoloai apply` errored when the target repo had uncommitted changes unless `--force` was passed.

**New behavior:** `yoloai apply` auto-stashes uncommitted changes before applying commits (via `git am --autostash`) and restores them afterward. The `--force` flag no longer exists.

**Rationale:** Users who start a sandbox with a dirty tree couldn't apply agent changes without first committing or stashing manually. The guard was designed to prevent accidental data loss, but `git am --autostash` provides the same safety automatically.

**Migration:** Remove `--force` from any `yoloai apply` invocations. If the stash pop produces conflicts, git will report them with instructions.

### Smoke test `--limited` flag replaced by `--full`

**Previous behavior:** `python3 scripts/smoke_test.py` ran the full backend matrix by default. `--limited` skipped unavailable backends instead of aborting.

**New behavior:** The default (no flag) runs a base tier with only the most reliable backends (docker + containerd-vm on Linux, docker + tart on macOS). `--full` enables the full backend matrix (adds podman, gVisor, vm-enhanced). Missing backends are always skipped with a warning, never an abort.

**Rationale:** Base tier is fast enough for PR gates and nightly CI. Full tier is for pre-release validation. The old `--limited` flag was backwards — it made the safe option the non-default.

**Migration:** Replace `--limited` with no flag (base tier). Replace bare invocation with `--full` for pre-release runs. `make smoketest` now runs base tier; `make smoketest-full` runs full tier.

### Profile system redesigned: `profiles/base/` replaced by `defaults/`, no default profile setting

**Previous behavior:** Profile defaults lived in `~/.yoloai/profiles/base/` (config.yaml, Dockerfile, entrypoint.sh, tmux.conf). The `profile` config key let users set a default profile applied to all new sandboxes without `--profile`. Profile config merged over base config (base → profile → CLI). Profiles referenced a parent via `extends: base`.

**New behavior:**
- `~/.yoloai/profiles/base/` is replaced by `~/.yoloai/defaults/` (config.yaml and optionally tmux.conf only — no Dockerfile or scripts).
- Baked-in defaults (Dockerfile, entrypoint scripts, initial config values) are embedded in the binary. They are not written to disk and are not user-editable.
- The `profile` config key is removed. There is no default profile. `--profile <name>` on `yoloai new` is always explicit; omitting it uses `defaults/`.
- Profiles are self-contained: their config.yaml merges over baked-in defaults only. User defaults (`defaults/config.yaml`) do not apply when a profile is active.
- Profile directories contain `config.yaml`, optionally `Dockerfile` (must use `FROM yoloai-base`), and optionally `tmux.conf`. Entrypoint scripts are not profile-overridable.
- The `extends` field is removed from profile config. All profiles inherit from baked-in defaults.
- `profile.yaml` renamed to `config.yaml` in profile directories.

**Rationale:** Profiles are now fully deterministic — their behavior depends only on the profile and baked-in defaults, not on the user's personal config. This eliminates "works on my machine" problems when sharing profiles. User defaults are clearly separate from profiles and only apply in the no-profile case.

**Migration:**
- Copy `~/.yoloai/profiles/base/config.yaml` to `~/.yoloai/defaults/config.yaml`.
- Remove the `profile` key from your config if set. Use `--profile <name>` explicitly with `yoloai new`.
- If you customized `profiles/base/Dockerfile` or `profiles/base/entrypoint.sh`, move those customizations into a named profile.
- Rename `profile.yaml` to `config.yaml` in any existing profile directories.
- Remove `extends: base` from any profile configs.
- The profile name `base` is no longer reserved.

### `backend` config key renamed to `container_backend`

**Previous behavior:** The `backend:` key in `~/.yoloai/config.yaml` selected the container runtime (docker, podman, …). Shipped in tags `v0.1.0` and `v0.1.1`.

**New behavior:** The same setting is now `container_backend:`. Shipped in `v0.2.0` and later.

**Rationale:** The unqualified `backend` name was ambiguous against the broader yoloAI concept of "backend" (which also covers `tart` and `seatbelt` — not all of them are container runtimes). `container_backend` names the actual scope.

**Migration:** Rename `backend:` to `container_backend:` in any `~/.yoloai/config.yaml` written by v0.1.x. No auto-migration; v0.2+ ignores the old key.

**History note:** Earlier revisions of this entry described a `--security` → `--isolation` flag rename and a `gvisor`/`kata`/`kata-firecracker` → `container-enhanced`/`vm`/`vm-enhanced` value rename. Verified via `git tag` audit that **none of those names ever appeared in a tagged release** — `--isolation` and the `container`/`vm` value spellings have been the public surface since `v0.2.0`. See `docs/contributors/design/findings-unresolved.md` DF1 for the audit. The flag/value text was removed in this revision to avoid misleading migrations.

### `sandbox <name> log` redesigned around structured JSONL

**Previous behavior:** `yoloai log <name>` (and `yoloai sandbox <name> log`) displayed the raw agent terminal output (`logs/agent.log`) with ANSI stripped by default. `--raw` preserved ANSI escape sequences. `--json` returned `{"content": "..."}`.

**New behavior:** Default view is a pretty-printed, merge-sorted stream of all four structured JSONL logs (`logs/cli.jsonl`, `logs/sandbox.jsonl`, `logs/monitor.jsonl`, `logs/agent-hooks.jsonl`). Agent terminal output is accessed via dedicated flags. `--raw` now emits raw JSONL lines.

New flags:
- `--agent` — show agent terminal output with ANSI stripped (replaces old default)
- `--agent-raw` — show raw agent terminal stream (replaces old `--raw`)
- `--raw` — emit structured log as raw JSONL (new meaning)
- `--source cli,sandbox,monitor,hooks` — filter to specific log sources
- `--level debug|info|warn|error` — filter by minimum level (default: `info`)
- `--since <duration|time>` — filter by timestamp (e.g. `5m` or `14:20:00`)
- `--follow` / `-f` — tail all sources live; auto-exits when sandbox is done

`--json` flag no longer has special handling for the log command.

**Rationale:** Structured JSONL is the primary log format — the pretty-printed interleaved view is far more useful than raw terminal output for debugging. Agent output is still accessible via `--agent`/`--agent-raw` for cases where it's needed.

**Migration:**
- `yoloai log <name>` — now shows structured log. Add `--agent` to see agent output as before.
- `yoloai log <name> --raw` — now emits raw JSONL. Use `--agent-raw` for old behavior.
- `yoloai log <name> --json` — no longer returns `{"content": "..."}`. Read `logs/agent.log` directly.

### Caret encoding updated to current spec

**Previous behavior:** Caret encoding used raw hex for all unsafe characters (e.g., `/` → `^2F`, `^` → `^5E`). `.` and `~` were encoded as unsafe. The decoder accepted `g/G`, `h/H`, `i/I`, `j/J` as hex width modifiers (3-6 digit forms).

**New behavior:** Caret encoding uses single-letter shortcuts where defined by the spec (e.g., `/` → `^s`, `^` → `^^`, `:` → `^k`, `@` → `^o`). `.` and `~` are now in the safe set (passed through unencoded), except `.` is still encoded when it's the last character of a path component (trailing dots are stripped by Windows). The decoder treats `g`–`v` (and uppercase) as shortcut codes, not hex width modifiers. Hex encoding (`^2F`, etc.) is still accepted by the decoder for backward compatibility.

**Rationale:** Aligns with the current caret-encoding spec (https://github.com/kstenerud/caret-encoding). Shortcuts produce shorter, more readable directory names. `.` and `~` are "Problematic" (position-dependent) in the spec, not "Reserved", and are shown unencoded in spec examples.

**Migration:** Existing sandbox directory names on disk will not match new encodings (e.g., `^2Fhome^2Fuser` → `^shome^suser`). Destroy and recreate affected sandboxes. No automatic migration — this is a pre-release change.

### `reset` redesigned: in-place default, cache/files cleared, new flags

**Previous behavior:** `yoloai reset` stopped and restarted the container by default. `--no-restart` kept the agent running (in-place reset). `--clean` wiped agent state and cache. `--clean` and `--no-restart` were mutually exclusive.

**New behavior:** `yoloai reset` resets in-place by default (agent stays running). Cache and files directories are cleared by default. New flags:
- `--restart` — stop and restart container
- `--clear-state` — wipe agent runtime state (replaces `--clean`, implies `--restart`)
- `--keep-cache` — preserve cache directory
- `--keep-files` — preserve files directory
- `--attach` now implies `--restart`

Automatic upgrades to `--restart`: overlay mode, container not running, or `--clear-state` set.

**Rationale:** In-place reset is the better default — it preserves agent context while syncing workspace changes. The old `--no-restart` flag required opting in to the better UX. Cache and files are now cleared by default for a clean slate, with `--keep-X` flags for opt-out. `--clean` mixed too many concerns (agent state + cache); `--clear-state` is more precise.

**Migration:**
- `yoloai reset <name>` — now resets in-place (was restart). Add `--restart` to stop and restart the container.
- `yoloai reset <name> --no-restart` — remove `--no-restart` (now the default).
- `yoloai reset <name> --clean` — replace with `--clear-state`. Note: cache is now cleared by default, so `--clear-state` only adds agent runtime state wipe.
- Cache/files are now cleared by default. Add `--keep-cache` and/or `--keep-files` to preserve them.

### `files` command: name before subcommand

**Previous behavior:** `yoloai files put <sandbox> <file>...` — subcommand before sandbox name.

**New behavior:** `yoloai files <sandbox> put <file>...` — sandbox name before subcommand.

**Rationale:** Name-first ordering is more ergonomic (name is the "context", action is the "verb") and consistent with top-level commands (`yoloai diff <name>`) that already put the name first.

**Migration:** Swap the sandbox name and subcommand positions. For example, `yoloai files put mybox file.txt` becomes `yoloai files mybox put file.txt`.

### `sandbox` command: name before subcommand

**Previous behavior:** `yoloai sandbox info <name>`, `yoloai sandbox log <name>` — subcommand before sandbox name.

**New behavior:** `yoloai sandbox <name> info`, `yoloai sandbox <name> log` — sandbox name before subcommand. `sandbox list` is unchanged (no sandbox name).

**Rationale:** Same as `files` — name-first ordering is more ergonomic and consistent with top-level commands.

**Migration:** Swap the sandbox name and subcommand positions. For example, `yoloai sandbox info mybox` becomes `yoloai sandbox mybox info`.

### `sandbox network` flattened to `allow`/`allowed`/`deny`

**Previous behavior:** Network allowlist management used nested subcommands: `yoloai sandbox network add <name> <domain>...`, `yoloai sandbox network list <name>`, `yoloai sandbox network remove <name> <domain>...`.

**New behavior:** Flattened to direct subcommands with name-first ordering: `yoloai sandbox <name> allow <domain>...`, `yoloai sandbox <name> allowed`, `yoloai sandbox <name> deny <domain>...`.

**Rationale:** Reduces nesting depth and uses clearer verb names (`allow`/`deny` instead of `add`/`remove`, `allowed` instead of `list`).

**Migration:** Replace `sandbox network add` with `sandbox <name> allow`, `sandbox network list` with `sandbox <name> allowed`, `sandbox network remove` with `sandbox <name> deny`.

### `sandbox clone` removed

**Previous behavior:** `yoloai sandbox clone <src> <dst>` was available as an alias for `yoloai clone`.

**New behavior:** Only the top-level `yoloai clone <src> <dst>` is available.

**Rationale:** The `sandbox clone` alias conflicted with the name-first dispatch pattern (where `clone` would be interpreted as a sandbox name). The top-level command is the canonical form.

**Migration:** Replace `yoloai sandbox clone` with `yoloai clone`.

### `files get` signature changed: positional destination replaced with `-o` flag

**Previous behavior:** `yoloai files get <sandbox> <file> [dst]` — single file, optional positional destination argument.

**New behavior:** `yoloai files get <sandbox> <file/glob>... [-o dir]` — multiple files/globs, destination specified via `-o`/`--output` flag (defaults to `.`).

**Rationale:** Positional destination prevented accepting multiple file arguments. The `-o` flag is a standard convention (`curl -o`, `tar -C`) and removes ambiguity between file arguments and the destination.

**Migration:** Replace `yoloai files get <sandbox> <file> <dst>` with `yoloai files get <sandbox> <file> -o <dst>`.

### Entrypoint shell scripts consolidated into Python

**Previous behavior:** Each backend had its own shell entrypoint script: `entrypoint-user.sh` for Docker, `entrypoint.sh` for seatbelt, and `setup.sh` for Tart. These scripts contained ~80 lines of near-identical logic for config reading, tmux setup, agent launch, ready-pattern detection, prompt delivery, and status monitoring.

**New behavior:** A single Python script `sandbox-setup.py` replaces all three shell scripts. Backend-specific setup is dispatched by a CLI argument (`docker`, `seatbelt`, `tart`). The script is embedded in the Go binary via `runtime/monitor/` and deployed identically to `status-monitor.py`. The Docker root entrypoint (`entrypoint.sh`) remains shell — it handles system-level setup (iptables, usermod, gosu).

**Rationale:** The duplicated shell logic meant every bug fix or feature change had to be applied three times. Shell is also fragile for the complex polling/state logic these scripts contain. Python provides `json.load()` (eliminating 8+ `jq` calls per script), proper string handling, and threading for background tasks.

**Migration:** If you customized `entrypoint-user.sh` in a Docker profile, port your changes to Python by modifying the `setup_docker()` function in `sandbox-setup.py`. Docker images must be rebuilt (`yoloai system build`).

### Legacy sandbox support removed

**Previous behavior:** Old sandboxes (created before the directory layout reorganization) were supported via automatic fallbacks to legacy file names (`meta.json`, `config.json`, `state.json`, `status.json`, `agent-state/`) and legacy file locations (PID files, tmux sockets, profile files at the sandbox root). Config migration from the old flat `~/.yoloai/` layout ran automatically on startup.

**New behavior:** Legacy fallbacks are removed. Only the current file names and directory layout are supported. Config migration from the old flat layout is removed. The `destroy` command always succeeds (returns nil if the sandbox directory doesn't exist, warns instead of failing on directory removal errors). Non-destroy commands that fail on a sandbox include the sandbox directory path and a `yoloai destroy` hint in the error message.

**Rationale:** Legacy support was causing recurring issues during sandbox start, reset, and destroy operations. During early development, maintaining backward compatibility with old sandboxes added complexity without sufficient benefit.

**Migration:** Destroy old sandboxes with `yoloai destroy <name>` and recreate them. If you have an old `~/.yoloai/config.yaml` with the `defaults:` nesting from the pre-profile layout, delete `~/.yoloai/` and run `yoloai setup`.

### Sandbox directory layout reorganized; `YOLOAI_DIR` abstraction added

**Previous behavior:** Sandbox state files had generic names (`meta.json`, `config.json`, `state.json`, `status.json`) in a flat layout. The `agent-state/` directory held agent runtime state. Docker hardcoded `/yoloai/` paths; seatbelt and tart used different variable names. Scripts, tmux config, and backend-specific files all lived at the sandbox root.

**New behavior:** Files are renamed for clarity and organized into subdirectories:
- `meta.json` → `environment.json`
- `config.json` → `runtime-config.json`
- `state.json` → `sandbox-state.json`
- `status.json` → `agent-status.json`
- `agent-state/` → `agent-runtime/`
- Scripts moved to `bin/` (entrypoint.sh, status-monitor.py, diagnose-idle.sh)
- Tmux config moved to `tmux/` (tmux.conf, tmux.sock)
- Backend-specific files moved to `backend/` (instance.json, profile.sb, pid, stderr.log)

All entrypoint scripts now use `$YOLOAI_DIR` instead of hardcoded paths. Docker sets `ENV YOLOAI_DIR=/yoloai`, seatbelt exports `YOLOAI_DIR=$SANDBOX_DIR`, tart exports `YOLOAI_DIR=$SHARED_DIR`.

**Rationale:** Generic names like `config.json` and `status.json` didn't convey purpose. The flat layout mixed scripts, configs, state, and backend files. Hardcoded `/yoloai/` paths in hook commands broke on seatbelt (where the sandbox dir is a host-local path, not `/yoloai/`).

**Migration:** Automatic. The code checks for new filenames first, then falls back to legacy names. Existing sandboxes continue to work. New sandboxes use the new layout. Docker images must be rebuilt (`yoloai system build`) for new sandboxes.

### Sandbox status `running` renamed to `active`; `--running` flag renamed to `--active`

**Previous behavior:** The agent status was `"running"` when actively working. `yoloai ls --running` filtered for active sandboxes.

**New behavior:** The agent status is `"active"`. `yoloai ls --active` filters for active sandboxes.

**Rationale:** `"running"` was ambiguous -- the container process is also "running" when the agent is idle. `"active"` clearly means the agent is actively working on a task.

**Migration:** Replace `--running` with `--active` in scripts. Old sandboxes with `"running"` in the agent status file are handled automatically (backward compatible parsing).

### `container_id` removed from JSON output

**Previous behavior:** `yoloai ls --json` and `yoloai sandbox info --json` included a `container_id` field in the output.

**New behavior:** The `container_id` field is no longer present.

**Rationale:** The field was always empty — it was never populated with a value. Removing it cleans up the JSON API.

**Migration:** Remove any code that reads `container_id` from yoloAI JSON output. The field was always empty, so no information is lost.

### `yoloai new` no longer auto-attaches by default

**Previous behavior:** `yoloai new` auto-attached to the tmux session after creation. `--detach`/`-d` skipped the attach.

**New behavior:** `yoloai new` starts the sandbox in the background (detached). Use `--attach`/`-a` to auto-attach. `--detach`/`-d` is removed.

**Also applies to:** `yoloai start` now supports `--attach`/`-a` with the same semantics (detached by default).

**Rationale:** Consistent unix-y model — both `new` and `start` are detached by default, both accept `-a` to attach. Avoids confusing asymmetry where `new` used `-d` (detach) while `start` used `-a` (attach).

**Migration:** Add `-a` to auto-attach: `yoloai new -a ...`. Remove `-d`/`--detach` from any scripts.

### Tmux mouse mode no longer enabled by default

**Previous behavior:** Sandbox tmux sessions had `set -g mouse on`, enabling mouse-wheel scrolling, click-to-select-pane, and drag-to-resize. OSC 52 clipboard and `MouseDragEnd1Pane` bindings were configured to compensate for mouse mode breaking copy/paste.

**New behavior:** Mouse mode is off. Text selection and copy/paste work normally via the terminal. Scrollback is accessed with `^b [` (shown in the status bar). The OSC 52 and MouseDragEnd workarounds are removed.

**Rationale:** Mouse mode breaks copy/paste in many terminal emulators, and the clipboard workarounds (OSC 52, MouseDragEnd1Pane pipe-and-cancel) don't work reliably across all setups. Broken copy is worse UX than needing a keybinding to scroll.

**Migration:** If you prefer mouse mode, add `set -g mouse on` to your own `~/.tmux.conf` or a custom profile's tmux config.

### `--backend` moved from global flag to per-command flag

**Previous behavior:** `--backend` was a global flag available on all commands.

**New behavior:** `--backend` is a local flag on `new`, `build`, and `setup` only. Lifecycle commands (`start`, `stop`, `destroy`, `reset`, `attach`, `exec`, `sandbox info`) read the backend from the sandbox's `meta.json` automatically. `list` uses the config default.

**Rationale:** The backend is a property of the sandbox, not the CLI invocation. Lifecycle commands should use the backend the sandbox was created with, not require the user to remember and pass it every time.

**Migration:** Remove `--backend` from lifecycle command invocations. If you were passing `--backend` to `start`/`stop`/etc., it now happens automatically via the sandbox's environment metadata.

### Config paths restructured: `defaults.` prefix removed, config moved to profile

**Previous behavior:** Config lived at `~/.yoloai/config.yaml` with settings nested under `defaults:` (e.g., `defaults.backend`, `defaults.agent`, `defaults.env.<NAME>`). Operational state (`setup_complete`) was stored in the same file.

**New behavior:** Config lives at `~/.yoloai/profiles/base/config.yaml` with a flat schema (e.g., `backend`, `agent`, `env.<NAME>`). Operational state moved to `~/.yoloai/state.yaml`. Resource files (Dockerfile, entrypoint.sh, tmux.conf) moved from `~/.yoloai/` to `~/.yoloai/profiles/base/`.

**Rationale:** Base config is now a profile — same structure and code path as user profiles. Flat schema is simpler and the `defaults:` wrapper added no value. Separating operational state from user preferences keeps config clean.

**Migration:** Automatic. On first run, yoloai detects the old layout and migrates: moves resource files to `profiles/base/`, flattens `defaults:` mapping to root level in `profiles/base/config.yaml`, extracts `setup_complete` to `state.yaml`. For manual config commands, drop the `defaults.` prefix (e.g., `yoloai config set backend docker` instead of `yoloai config set defaults.backend docker`).

### `tmux_conf` and `model_aliases` moved to global config

**Previous behavior:** `tmux_conf` and `model_aliases` were stored in the base profile config (`~/.yoloai/profiles/base/config.yaml`).

**New behavior:** These settings are stored in the global config (`~/.yoloai/config.yaml`), which is not overridable by profiles.

**Rationale:** `tmux_conf` and `model_aliases` are user preferences that should apply to all sandboxes regardless of profile. They don't belong in profile-overridable config.

**Migration:** Automatic. On first run, yoloai moves `tmux_conf` and `model_aliases` from the base profile config to the global config. No manual action needed.

