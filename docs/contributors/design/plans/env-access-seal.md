# Env access seal — `config.HostEnv` curated accessors

ABOUTME: Handoff/plan for replacing the ad-hoc `config.Layout` env accessors
ABOUTME: with a single opaque, purpose-method, forbidigo-gated curation type.

Status: **IMPLEMENTED** on branch `df19-test-isolation` (commits `2f9e4fd`
Phase 1, `2dcb9aa` Phase 2, `04c4c32` Phase 3, `764556d` Phase 4; `make check`
green at each). The locked design below was followed; deviations recorded in
§8 at the bottom. (Superseded the first encapsulation pass in `9223058`.)

---

## 1. Where we are now (already committed — `9223058`)

The previous turn sealed `config.Layout.Env` (the host-env snapshot) so it's no
longer a public map. Current state in `internal/config/layout_env.go`:

- The field is unexported: `Layout.env map[string]string`.
- Accessor methods on `Layout`:
  - `LookupEnv(key) (string,bool)` — one arbitrary key
  - `ExecEnv(allow []string, overrides map) []string` — caller-supplied allowlist
  - `CuratedEnv(allow []string) map` — caller-supplied allowlist, map return
  - `GitEnv() []string` — wraps `sysexec.GitEnv` (fixed allowlist) — the ONE good one
  - `EnvForExtension() []string` — full passthrough (`yoloai x`)
  - `WithEnv(map) Layout` — edge setter
  - `EnvSnapshot() map` — embedder/diagnostics full-map getter
  - `EnvProfile`/`MapEnv`/`EnvLookup` interface
- `runtime.DaemonEnvVars` is the daemon-discovery allowlist union (in `runtime/probe.go`, re-exported as `yoloai.DaemonEnvVars`).
- forbidigo gates: `EnvSnapshot`, `EnvForExtension`, `testutil.GetCuratedHostEnv` (deny-by-default + reviewed allowlists in `.golangci.yml`).
- Test edge: `testutil.HostEnv()` → `testutil.GetCuratedHostEnv(allow)` + shared `testutil.IntegrationHostEnvVars`.

**Why this is not good enough (the user's critique):** these accessors just
relocate the problem. `LookupEnv` is `os.Getenv` in disguise (used all over).
`ExecEnv`/`CuratedEnv` let any caller declare "I need these keys" inline at the
call site — the curation decision is still made by whoever writes the call, not
centrally. `EnvSnapshot`/`EnvForExtension` are "give me everything" methods.
`GitEnv` is the only one doing it right: **the decision of what's needed is made
outside the consuming code, and the caller just names a purpose.**

---

## 2. The locked design

### Principle

Env access is by **named purpose**. The keyset for each purpose is a decision
made **outside** the consuming code (centralized, reviewable), exactly like
`GitEnv`. Consuming code names the purpose; it never enumerates keys inline.
"Coarse is OK" — if a caller only needs 1 of 3 allowlisted values, that's fine
if it keeps the design tractable.

### The type

An **opaque `config.HostEnv`** type holds the captured snapshot (`vars`) plus
the `homeDir` it needs to compute overrides (e.g. `HOME=homeDir`, which under
`sudo` differs from the snapshot's `HOME`; `TART_HOME` default). Reached via
`layout.Env()` (returns a `HostEnv`). It owns **all** env access and curation;
the per-purpose allowlists move *into* this type from the backend packages
(centralized — the user explicitly wants this).

Built from `ClientCreateOptions.Env`: the CLI seeds that from the host
(`processEnv()` at the cliutil edge), other embedders seed however they want.

### Accessors — all purpose-named, no-arg (except agent creds), all forbidigo-gated

Family A — yoloAI's own host-side subprocess envs (yoloAI owns the allowlist,
user has no say, no isolation concern):

| Method | Returns | Replaces (current call sites) |
|---|---|---|
| `EnvForGitInvocation()` | `[]string` | `GitEnv()` — git.NewHost/NewSandbox |
| `EnvForDockerExec()` | `[]string` | docker.go:234 (`dockerExecAllowlist`) |
| `EnvForDockerBuild()` | `[]string` | `CuratedBuildEnv` (`buildEnvAllowlist`) |
| `EnvForContainerdExec()` | `[]string` | containerd.go:135 |
| `EnvForSeatbeltExec()` | `[]string` | seatbelt.go:152 |
| `EnvForSeatbeltSandbox()` | `[]string` | seatbelt.go sandboxEnv (`sandboxEnvAllowlist`) |
| `EnvForTartInvocation()` | `[]string` | tart.go:42/244 (`tartEnvAllowlist`) + TART_HOME override |
| `EnvForDaemonDiscovery()` | `map` | docker daemon, podman `discoverSocket`, `SelectBackend` (`DaemonEnvVars`) |
| `EnvForHostTool()` | `[]string` | the 6 inline `[]string{"PATH","HOME","TMPDIR"}` sites: terminal tmux, bugreport tmux, vscode, files cp ×2, reset rsync, diagnostics uname (uses PATH/HOME — TMPDIR harmless) |
| `EnvForDiagnostics()` | `map` | diagnostics.go `diagnosticEnvVars` loop |
| `EnvForConfigInterpolation()` | `map` | feeds `${VAR}` expansion — see below |
| `PassthroughEnv()` | `[]string` | rename of `EnvForExtension` — `yoloai x` only |

`EnvForAgentCredentials(declaredKeys []string) map[string]string` — the ONE
accessor that takes an argument. Caller passes the agent definition's
`APIKeyEnvVars` / `AuthHintEnvVars`. Returns the present (non-empty) subset.
Justified: credentials are inherently per-agent, the keys are *declared data*
from the agent def (not an inline literal), and the gate forces review. This
keeps `config` **agent-agnostic** (no `config → agent` import). Replaces the
`LookupEnv` loops in `provision.go` (HasAnyAPIKey/HasAnyAuthHint/CreateSecretsDir/
shouldSkipSeedFile/SeedSandbox) and `system.go:517`.

### `${VAR}` expansion — separate concern, consumes only a curated accessor

Expansion stays a separate string function (`expandEnvBraced`/`ExpandPath`), NOT
a method on `HostEnv`. It receives its map *only* from
`EnvForConfigInterpolation()` — never the raw env. So in code:
`expandEnvBraced(value, layout.Env().EnvForConfigInterpolation())`.

`EnvForConfigInterpolation()` returns a **fixed allowlist**: `HOME`, `USER`,
`LANG`, `LC_*` (prefix match — the allowlist mechanism must support prefixes),
`TZ`. This **closes the last arbitrary reader**: `${SECRET_KEY}` in a config
value no longer resolves. `EnvLookup` and `LookupEnv` are **deleted** (the only
legitimate arbitrary-key consumer, expansion, now goes through this accessor).

This is a **behavior change** → `docs/BREAKING-CHANGES.md` entry required:
`${VAR}` interpolation in config/profile values now resolves only the
allowlisted vars above; previously any process env var resolved.

### Gating

Every `EnvFor…` method and `PassthroughEnv` is **forbidigo deny-by-default**,
with a per-call-site reviewed allowlist in `.golangci.yml`. So every env touch
in the codebase must justify which `EnvForXYZ` it calls and why. (Calls inside
package `config` are unqualified and won't match a `^pkg\.Method$`-style pattern;
for methods, use `\.EnvForXYZ$` patterns and path-scope the exclusions — see the
existing `EnvSnapshot`/`EnvForExtension` gates as the template.)

### `EnvSnapshot` removal

Delete it. The full map crosses only at the **public `ClientCreateOptions.Env`
boundary** (an embedder hands the library its captured env — unavoidable and
correct). For the CLI, keep the raw `processEnv()` map at the cliutil edge and
feed `ClientCreateOptions.Env` from there, instead of round-tripping through the
Layout. (Verify cliutil plumbing: `cliutil.rootLayout` is set via
`SetRootLayout`/`SetRootLayoutFromFlag`; `client.go` reads it to build
`ClientCreateOptions`. May need a package-level `rootEnv` map at the edge.)

---

## 3. Explicitly SHELVED (YAGNI — do NOT build; "until people ask")

The user felt no pain with current behavior, so these speculative pieces are
out of scope:

- **`EnvForAgent(agentType)` host→sandbox env passthrough + `*` wildcard.** This
  was a genuinely new feature: letting host env vars flow *into* the sandbox for
  the agent (today nothing from the host reaches the agent — `ContainerEnv` is
  just `LANG=C.UTF-8`, config `env:` and credentials are injected as files under
  `/run/secrets`, not env vars). It has isolation/security weight (`*` pours host
  secrets across the sandbox boundary) and deserves its own design pass if
  revived. Family A above is host-side only and unrelated.
- **User-extensible interpolation allowlist** (config/profile override to add
  vars to `EnvForConfigInterpolation`). Only existed to serve
  `HOST`/`PORT`/`DATABASE_URL`. When someone asks to interpolate a non-default
  var, *that* is the trigger to build this. Note: shipping the fixed allowlist
  WITHOUT this escape hatch means arbitrary interpolation hard-breaks with no
  opt-in — accepted consciously (no one has reported relying on it).

---

## 4. Implementation plan (4 phases, `make check` green between each)

Phase ordering matters: the field is already private, so the type + accessors
go in additively first, then migrate, then delete the old methods, then gate.

1. **Define `config.HostEnv` + all `EnvFor…` accessors** (additive; old methods
   still present). Move the backend allowlists (`dockerExecAllowlist`,
   `buildEnvAllowlist`, `containerdExecAllowlist`, `tartEnvAllowlist`,
   `sandboxEnvAllowlist`, `runtime.DaemonEnvVars`, the `["PATH","HOME","TMPDIR"]`
   host-tool set, `diagnosticEnvVars`, interpolation set) into `config` as the
   data backing each accessor. `layout.Env()` returns a `HostEnv`. Build green.
2. **Constrain `${VAR}` expansion** to `EnvForConfigInterpolation()` output;
   delete `EnvLookup`/`LookupEnv`; add the `BREAKING-CHANGES.md` entry. (`map`
   doesn't satisfy any interface now — expansion takes the curated map directly.)
3. **Migrate all call sites** to the `EnvFor…` accessors; rename
   `EnvForExtension`→`PassthroughEnv`; delete `ExecEnv`/`CuratedEnv`/`GitEnv`/
   `EnvSnapshot`. Drop `EnvSnapshot` via the cliutil-edge raw map.
4. **forbidigo-gate** every `EnvFor…`/`PassthroughEnv` method with a per-call-site
   allowlist. Final verification.

Call-site inventory to migrate (from the current tree; grep to confirm):
- `LookupEnv` (14): provision ×5, invocation ×1, system.go ×1 (→ agent-creds);
  diagnostics ×1 (→ EnvForDiagnostics); pathutil ×1 (→ interpolation); build.go
  ×1 (→ EnvForDockerBuild); tart ×2 (TART_HOME → EnvForTartInvocation override);
  terminal TMUX ×1, log COLUMNS ×1, prepare_dirs ×1 (agent auth-hint).
- `ExecEnv` (13): 6 named-allowlist (docker/containerd/seatbelt×2/tart×2) →
  per-backend accessors; 7 inline → `EnvForHostTool` (+ diagnostics uname).
- `CuratedEnv` (8): all `DaemonEnvVars` → `EnvForDaemonDiscovery`.
- `EnvForExtension` (1): xcmd/x.go → `PassthroughEnv`.
- `EnvSnapshot` (4): cliutil/client.go, lifecycle/new.go, sandboxcmd/bugreport.go
  → cliutil-edge raw map for `ClientCreateOptions.Env`.

TMUX/COLUMNS are not subprocess envs — give them honest named accessors
(`InHostTmux() bool`, `TerminalColumns() (int,bool)`) rather than forcing them
into the EnvFor… family. TART_HOME folds into `EnvForTartInvocation`'s overrides.

---

## 5. Open specifics to settle during implementation

- **`config` home for the type vs new `internal/env` package.** Plan assumes
  `config` (the Layout carrier; allowlists are just string data, no imports).
  Reconsider only if it bloats `config` awkwardly.
- **Override computation** (`HOME=homeDir`, `TART_HOME`) needs `homeDir` inside
  `HostEnv`. Carry it in the type (`HostEnv{vars, homeDir}`); `layout.Env()`
  builds `HostEnv{vars: l.env, homeDir: l.HomeDir}` on demand (cheap).
- **`LC_*` prefix matching** in the interpolation allowlist (exact-match won't
  cover `LC_ALL`/`LC_CTYPE`/…).

---

## 6. Verification (the gate)

- `make check` is the authority. It runs gofmt, **pinned golangci-lint v2.11.3**,
  `go mod tidy` check, vet-tagged, tests, python.
- **Lint must use the pinned version**, not ambient:
  `GOTOOLCHAIN=$(go env GOVERSION) go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3 run ./...`
  An ambient (newer) golangci-lint flags pre-existing camelCase `sloglint` keys
  in the Tart package that the pinned gate does NOT — those are NOT yours; don't
  chase them.
- `go vet -tags 'integration e2e' ./...` (the `vet-tagged` step) compiles the
  tagged test files; forbidigo does NOT analyze tagged files by default, so only
  non-tagged callers need gate allowlist entries.
- If the SUT/exec env path changes, run `make e2e` (Docker required).

Gotcha already hit: a `create`-package test relied on the developer's ambient
`ANTHROPIC_API_KEY` flowing in via the old full snapshot. `layoutForTmpDir` now
allowlists agent-credential vars from the registry (`agentCredentialEnvVars()`
in `fakeruntime_test.go`). Keep that hermetic.

---

## 7. Key files

- `internal/config/layout_env.go` — the accessors live here (rework target).
- `internal/config/layout.go` — `Layout` struct (`env` field, `WithEnv`).
- `internal/config/pathutil.go` — `expandEnvBraced`/`ExpandPath` (expansion).
- `runtime/probe.go` — `DaemonEnvVars`.
- backend allowlists to move: `runtime/docker/{docker,build}.go`,
  `runtime/containerd/containerd.go`, `runtime/seatbelt/seatbelt.go`, `runtime/tart/tart.go`.
- `internal/envsetup/envsetup.go` — agent-cred `LookupEnv` loops.
- `internal/cli/cliutil/{layout,client}.go` — the edge (`processEnv`, ClientCreateOptions).
- `.golangci.yml` — forbidigo `forbid` + `exclusions.rules` (gate template).
- `docs/BREAKING-CHANGES.md` — add the interpolation-constraint entry.

---

## 8. Deviations from this plan (as implemented)

The locked design held; these are the points where the implementation chose a
different (documented) path than the prose above:

- **Type home.** `config.HostEnv` lives in a new `internal/config/host_env.go`,
  not in `layout_env.go`. `layout_env.go` was deleted entirely; `WithEnv` moved
  into `host_env.go`. (§5's "reconsider only if it bloats config" — a sibling
  file read cleaner than reworking the old file in place.)
- **`EnvLookup`/`LookupEnv`/`MapEnv` deletion moved Phase 2 → Phase 3.** The
  plan's Phase 2 named the deletion, but those symbols were still used by the
  agent-credential / docker-build / model-prefix / TART_HOME / TMUX/COLUMNS /
  diagnostics readers, so deleting them in Phase 2 could not compile. Phase 2
  constrained `${VAR}` expansion (its real goal); Phase 3 migrated those
  remaining consumers and then deleted the interface + methods. End state is
  identical to the plan.
- **`runtime.DaemonEnvVars` / `yoloai.DaemonEnvVars` KEPT (not moved into
  config).** They are public API: external callers of the public
  `yoloai.SelectBackend` pass them. `config` cannot import `runtime` (import
  cycle), so it carries its own `daemonEnvAllowlist` (backing
  `EnvForDaemonDiscovery`) that mirrors the public list — a `keep the two in
  sync` comment marks both. Internal callers all use `EnvForDaemonDiscovery`.
- **`EnvSnapshot` removal** uses `cliutil.EdgeEnv()` (returns the
  `processEnv()` snapshot captured at the CLI edge) to feed
  `ClientCreateOptions.Env`; no new test-setter plumbing was needed because the
  `LayoutForDataDir`-based test helpers already build their Layout from the same
  `processEnv()`.
- **Seatbelt allowlists.** `seatbeltExecAllowlist` and `sandboxEnvAllowlist`
  were byte-identical, so `EnvForSeatbeltExec`/`EnvForSeatbeltSandbox` share one
  backing var (`seatbeltSandboxAllowlist`) — two methods, same data.
- **Gating granularity.** Phase 4 uses two broad `forbid` patterns
  (`\.EnvFor[A-Z]\w*`, `\.PassthroughEnv`) rather than one per method, with one
  `exclusions.rules` entry per purpose (grouping the allowed paths). Tests are
  exempt from the accessor gate (they read the threaded, test-controlled Layout,
  not ambient env); `os.Environ`/`exec.Command` stay banned in tests.
