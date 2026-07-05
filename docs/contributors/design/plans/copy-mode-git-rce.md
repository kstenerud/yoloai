# Plan: close the copy-mode host-git RCE (audit C1, CRITICAL)

ABOUTME: Design for neutralizing host code execution via git filter/diff/fsmonitor
ABOUTME: drivers in the agent-controlled copy-mode work-copy .git — the diff/apply path.

Status: **IMPLEMENTED for the container backends (docker/podman/containerd),
2026-06-29.** Surfaced by the 2026-06-29 escape/exfil security audit (finding C1,
CRITICAL, empirically reproduced). The recommended approach below shipped:
work-copy `add`/`diff`/`status`/`log`/`format-patch` now run in-confinement via
each backend's `GitExec`, dispatched by `runtime.GitRunsInConfinement` behind
`git.NewSandbox`. Verified on real Docker **and** Podman (malicious-filter case:
host marker never created; legit clean-filter case: diff byte-correct) — see
`internal/orchestrator/integration_test.go`.

**Update (2026-07-05): the two macOS backends are now also closed.** The
2026-06-29 fix left **apple** and **seatbelt** running work-copy git host-side —
apple was omitted from earlier drafts of this status entirely, and it was a fully
open, undocumented RCE (apple already exposed `container exec` but never
implemented `GitExecer`). Both are now fixed and confined
(`confine-host-side-git.md`, `confine-host-side-git-macos-build.md`): apple
dispatches work-copy git into its per-container VM (`GitExecInConfinement`);
seatbelt wraps git under a dedicated tight `sandbox-exec` SBPL profile
(`seatbelt.GenerateGitProfile`) that denies `mach-lookup`, scopes write to the
work copy, and confines `process-exec` to tool dirs. Both are verified on real
hardware (`internal/orchestrator/integration_macos_test.go`, teeth-checked; the
seatbelt profile's containment battery in `runtime/seatbelt/gitprofile_test.go`).

**Neutralization gap to keep in mind:** the *host-side* hardening
(`runtime.GitHardeningArgs` = `-c core.hooksPath=/dev/null -c
core.fsmonitor=false`) disables hooks and fsmonitor but **leaves
filter/textconv drivers live** — those are attribute-bound and must run for diff
correctness (Git LFS, git-crypt). That is safe **only** where git runs
in-confinement (`GitRunsInConfinement`). On a still-host-side path it is a live
RCE — which is exactly why apple/seatbelt had to move git into confinement rather
than "hardening" the host call.

**Residual:** `status.DetectChanges`/`ProbeWorkData` runs host `git status` on
the work copy for **broken-metadata** sandboxes only — a clean filter can still
fire there (fsmonitor is off, but filters remain attribute-bound); tracked as a
follow-up finding. Exploitability is low (`.meta` lives outside the sandbox, so
the agent cannot corrupt it to trigger the path). With that noted, C1 is closed
across all backends.

**Recommendation: run the work-copy git inside the agent's confinement
(in-container / in-VM / under the seatbelt profile), NOT on the host.** An earlier
draft recommended a host-side "private git-dir + clean config" approach; that is
**rejected** below because it silently corrupts diffs for any repo using legitimate
git filters (Git LFS, git-crypt, …). See "Rejected: Approach A".

## The vulnerability

yoloAI's copy/diff/apply copies the user's repo into a sandbox work copy; the
agent edits it; then `yoloai diff` / `apply` / `status` run **git on the host**
against that work copy to compute what changed. In `:copy` mode the work copy's
`.git/` (the project's real history — deliberately preserved so the agent can
`git log/blame`, see `prepare_dirs.go:330`) is bind-mounted **read-write** into
the container, so the in-sandbox agent fully controls `.git/config` and
`.gitattributes`.

Git executes shell commands defined in repo-local config, not only from
`.git/hooks/`:

- `filter.<name>.clean` / `.smudge` — run on `git add` when `.gitattributes` tags a path `filter=<name>`.
- `diff.<name>.command` / `.textconv` — run on `git diff`.
- `core.fsmonitor` — run on `git status`.

The host git runner (`internal/git/git.go:93`, `hostExec.run`) neutralizes
**hooks only** (`-c core.hooksPath=/dev/null`). It does nothing about the three
driver classes above. The host runs these in the agent-controlled work copy:

- `copyflow/diff.go:132` `git add -A` (every `yoloai diff`) → **clean filter fires**
- `copyflow/diff.go` / `apply.go:366` `git diff <baseline>` → **ext-diff/textconv fires**
- `internal/git/ops.go` `StageUntracked` (`add -A`), `CheckDirtyRepo` / `BaselineUncommittedChanges` (`status`) → **clean filter / fsmonitor fire**

So a malicious or prompt-injected agent writes a filter into the work copy
(`git config filter.x.clean '<cmd>'` + `printf '* filter=x\n' > .gitattributes`),
and when the user later runs `yoloai diff`/`apply`/`status`, `<cmd>` executes **as
the host user, outside the sandbox** — full host code execution. Reproduced on git
2.43.0 with the production `-c core.hooksPath=/dev/null` flag:
`git add -A` → clean fired; `git diff HEAD` → diff fired; `git status` → fsmonitor fired.

The **sandbox→host apply** path (`git apply`/`git am` into the user's real target
repo, `internal/git/ops.go`) is **not** affected: that repo's `.git/config` is
user-owned (the agent can't define drivers there), hooks are killed, and
`--unsafe-paths` is gated. Confirmed sound by the audit.

## The fundamental tension (why this is not a one-liner)

A correct diff of a repo that uses filters **requires running those filters** —
`git diff <commit>` normalizes the working-tree side *through* `filter.clean`
before comparing it to the committed (already-cleaned) blob. So:

- Running the filters on the host → **is the vulnerability** (arbitrary host exec).
- Not running them on the host (a "clean config") → **wrong, corrupting diffs**
  for every filtered repo (see Approach A's reproduction).

git cannot distinguish a malicious filter from a legitimate one — they are the same
mechanism. Therefore **"host-side AND secure" and "host-side AND correct-for-filtered-repos"
are mutually exclusive.** The only way to get both correctness and security is to
**move the execution off the host**, into the same confinement the agent already
runs in. That is the recommended approach.

This is a **GEN §14** decision ("clever hacks bite — lean on a foreign system's
contract, not an incidental property"). git's *contract* is that diff normalizes the
working tree *through* the configured filters; any host-side fix that stays secure
must subvert that contract (starve git of config), which is the brittle hack §14
warns against — it breaks the moment a repo uses filters as intended. The
contractual move is to let git do its whole job and place the *isolation* on the
boundary we own (the agent's confinement), not on tricking git. §14's "put the data
where it belongs" reads here as **put the execution where it belongs**: the work
copy is the sandbox's, so its git runs in the sandbox.

## Recommended: run work-copy git in the agent's confinement

Route the copy-mode work-copy git operations (`add`/`diff`/`status` and the
patch-generating `diff` in `GeneratePatch`) through the **agent's runtime**, not
host `hostExec`:

- Real git runs with the repo's real config in the sandbox → **filters apply
  consistently on both sides → correct diffs** (LFS, git-crypt, custom drivers all
  behave exactly as today).
- A malicious filter also runs — but **inside the already-compromised sandbox**,
  never on the host → **no escape** (the agent can already run anything there).
- yoloAI harvests only the **textual diff** over stdout. Apply still applies that
  patch to the user's real target repo via host git (`NewHost`, user-owned `.git`)
  — unchanged and already sound.
- The baseline SHA lives in the in-sandbox `.git` (the same `workDir/.git` via the
  mount), so in-sandbox git resolves it natively — **no alternates, no private
  git-dir, no baseline duplication.**

This is exactly what the **Tart** backend already does today: Tart is a
`SandboxSide` backend (`runtime.LocalityOf` → `LocalitySandboxSide`), so
`git.NewSandbox` dispatches its work-copy git through `sandboxExec` (in-VM). The
fix generalizes that: the work copy is logically "inside the sandbox" for git
purposes on **every** backend, so its git must run there.

### Per-backend coverage

| Backend | Today | Under the fix |
|---|---|---|
| **tart** (macOS VM) | SandboxSide — git runs in-VM | unchanged; already correct + secure |
| **docker / podman / containerd** | HostSide — host git on the work copy | route work-copy git through container exec (`docker exec` / `nerdctl exec`) |
| **seatbelt** (macOS) | HostSide — host git **outside** the sandbox (worst case) | **the hard case** — see below |

**Seatbelt** has no container to exec into; the work copy lives on the host FS and
the agent runs under `sandbox-exec`. To contain the work-copy git there, run it
**under the seatbelt profile** (the agent's confinement) so a malicious filter is
bounded by the same SBPL rules as the agent. This is only as strong as that
profile — and the audit's caps-F5 finding flags the current profile as too
permissive (unrestricted `mach-lookup`/`process-exec`). So seatbelt needs **both**
a git-under-`sandbox-exec` wrapper **and** the F5 profile tightening; until then it
carries a documented residual (and is macOS-only, the "lighter" isolation tier).

### Costs / behavior changes (honest)

- **The sandbox must be running** for copy-mode `diff`/`apply`/`status` (today a
  *stopped* copy-mode sandbox is diffable host-side). Mitigation: auto-start the
  sandbox for these verbs, or fail with a clear "start the sandbox to diff/apply"
  message. There is precedent: `:overlay` diff/apply **already** require the runtime
  (`ErrOverlayRequiresRuntime`), so the UX pattern and user expectation exist.
- A container-exec per git op (small constant cost).
- The in-sandbox git version is the profile image's, not the host's — acceptable
  (it is the repo's own environment, and the agent already used it).

### Implementation surface (concrete, ordered — worked out 2026-06-29)

**The path mapping (the crux).** The work copy is bind-mounted: host
`store.WorkDir(sandboxDir, hostPath)` = `<sandboxDir>/work/<EncodePath(hostPath)>`
↔ container `DirSpec.ResolvedMountPath()` (the `mountPath`, else the mirrored
`hostPath`). Two views of the **same files**. Host git runs `-C <host work copy>`;
**in-confinement git must run `-C <ResolvedMountPath>`** (the container view). The
diff/status/patch callers already load the meta and hold `dir.MountPath`
(`copyflow/diff.go:loadDiffContext`, `copyGitWorkDir` at `diff.go:376`), so the
container path is available at the call site. **This is why the path change is
entangled with the dispatch change** — today `loadDiffContext` passes the *host*
work path to `git.NewSandbox(...).Run`; the in-confinement path must pass the
*container* path instead. (Tart sidesteps this with a mechanical host→VM rewrite
`translateWorkDirToVMPath`; docker's mapping is meta-dependent, so resolve the
container path at the copyflow caller — do **not** couple `runtime` to `store`.)

Steps:
1. **`GitExec` on docker/podman/containerd** — mirror `runtime/tart/tart.go:552`
   exactly: resolve instance name (`store.InstanceName`), `isRunning` else
   `runtime.ErrNotRunning`, build `git -c core.hooksPath=/dev/null -C <containerPath> <args>`,
   run via the engine's raw exec (`docker exec`; preserve exact stdout — patches are
   whitespace-sensitive, see Tart's `ExecRaw` note), return stdout. Each declares
   `runtime.GitExecer`. Unit-testable: assert the argv + path with a fake engine.
2. **Dispatch** — in `internal/git/git.go` `NewSandbox`, route the container
   backends' work-copy git through `sandboxExec`/`GitExec` (today gated on
   `LocalitySandboxSide`). Cleanest: a new predicate "git runs in confinement"
   (true for SandboxSide **and** the container backends) rather than overloading
   `FilesystemLocality` — decouple *git-exec locality* from *filesystem locality*
   (update the `FilesystemLocality` doc at `runtime/runtime.go:260` accordingly).
   The host-**target** apply ops keep `NewHost`/`hostExec` (user-owned repo, safe)
   — do **not** reroute those.
3. **Pass the container path** — at the copyflow call sites, give the
   in-confinement execer `dir.ResolvedMountPath()` (not the host work copy).
4. **"Sandbox must be running"** — propagate `runtime.ErrNotRunning` to
   `diff`/`apply`/`status`; auto-start or clear error at the copyflow/CLI boundary
   (reuse the `ErrOverlayRequiresRuntime` precedent + UX).
5. **seatbelt** — `sandbox-exec`-wrapped git + the caps-F5 profile work (separate),
   or a documented residual (macOS-only, lighter tier).

**As shipped (deviations from the steps above).** Two refinements landed during
implementation:

- **Path mapping centralized in `git.sandboxExec`, not at the copyflow callers.**
  Rather than change every copyflow/orchestrator call site to pass
  `ResolvedMountPath`, `git.NewSandbox`'s `sandboxExec` loads the sandbox record
  once per op and maps the host work-copy path (`store.WorkDir`) → the dir's
  in-sandbox mount path itself (`confinementWorkPath`). Call sites keep passing
  the host path uniformly (the two `loadDiffContext`/`copyGitWorkDir` helpers were
  simplified to always return `store.WorkDir`); the locality knowledge lives in
  exactly one place. This keeps `runtime` decoupled from `store` — the mapping
  runs in `internal/git`, which may import `store`.
- **`GitExecer` gained a `user` param and takes the resolved instance name.**
  `GitExec(ctx, instance, user, workDir, …)`: `sandboxExec` resolves the instance
  (`store.InstanceName`) and the agent's container user (`store.ContainerUser`,
  the same identity the overlay exec uses) and passes them down, so the
  in-container git writes the index/objects with the ownership the agent expects.
  Tart ignores `user`.
- **Dispatch predicate is `runtime.GitRunsInConfinement`** (`SandboxSide` ∪ the
  `BackendCaps.GitExecInConfinement` flag set by docker/podman/containerd), exactly
  as proposed. The "sandbox must be running" UX is surfaced as a clear message at
  the public `Workdir` boundary (`Diff`/`Changes`/`Apply`/`Commits`), preserving
  `errors.Is(err, runtime.ErrNotRunning)` for SDK callers; `status`/`list` keep
  degrading to "unknown" via the existing `workprobe` path.

- Do **not** add `--no-ext-diff`/`-c core.fsmonitor=`/clean-config tricks: in
  confinement we *want* the real filters to run (correctness).
- Verification harness on this host: `runtime/docker/integration_test.go`,
  `internal/orchestrator/integration_test.go` (real Docker+Podman present;
  diff/apply correctness needs no API key). Add a malicious-filter case (host file
  NOT created) + a legit-filter case (diff correct) to the copy-mode diff/apply
  integration tests.

### Must verify on real backends

`make check` does not exercise real diff/apply. Verify on real Docker + Podman
(and a filtered repo!): byte-identical diff output vs today for binary diffs,
`--stat`/`--numstat`/`--name-only`, path filters, the `:copy` aux-dir pipeline, the
dirty-baseline (`BaselineUncommittedChanges`) case, **and a repo that uses a clean
filter / Git LFS** (where Approach A would corrupt). Confirm apply of an
in-sandbox-generated patch lands correctly on the host target repo.

## Rejected: Approach A — host-side private git-dir + clean config

Keep the host running the work-copy git, but point it at a host-private git-dir
(`--git-dir=<private> --work-tree=<workDir>`) with a **clean config** and reach the
original objects via `objects/info/alternates` → `workDir/.git/objects`. The clean
config has no agent-defined drivers, so malicious filters are inert; the alternates
let it resolve the baseline without copying objects.

**Why rejected — it silently corrupts every filtered repo.** The clean config
disables *all* config-defined filters, including legitimate ones, and the
committed baseline blobs are stored in *cleaned* form while the work tree is read
*raw* → an asymmetric, wrong diff. Reproduced (a normal `filter.redact.clean`,
only edit is `123`→`456`):

```
TODAY (filter applied to both sides):       FIX A (clean config, work-tree raw):
  -api_key = REDACTED-123                      -api_key = REDACTED-123
  +api_key = REDACTED-456   ← correct          +api_key = SECRET-456    ← WRONG + leaks raw secret
```

For **Git LFS** this is catastrophic: the baseline side is a small pointer, the
work-tree side is the full raw blob → the generated patch replaces pointers with
raw content (megabytes of garbage) and breaks the target repo's LFS on apply. Same
class of corruption for **git-crypt** (plaintext leaks into patches), keyword
expansion, redaction, and custom `textconv`/`diff.command` drivers.

A could only be made safe by *detecting* repos that use filters and refusing or
falling back — which is strictly worse than just running git in-confinement (the
recommended approach). Secondary gaps it also left open: a fresh private git-dir has
an unborn `HEAD` so `git status`/`CheckDirtyRepo` misreports unless `HEAD` is set to
the baseline; and the agent can `git gc --prune=now` in `workDir/.git` to evict the
baseline objects the alternates depend on (a diff DoS).

## Complementary cheap hardening (defense-in-depth, not a fix)

Independent of the above, the host **target**-repo git (`NewHost`) already disables
hooks; that stays. The in-confinement work-copy git deliberately does **not**
disable filters (it needs them for correctness, and they are contained). No
host-side `--no-ext-diff`/clean-config band-aids — they were an Approach-A artifact
and do not address the `git add` clean-filter on the host anyway.

## Pointers

- Sink: `internal/git/git.go:93` (`hostExec.run`, hooks-only neutralization);
  dispatch `git.NewSandbox` / `runtime.LocalityOf` / `FilesystemLocality`.
- Triggers: `copyflow/diff.go:132`, `copyflow/apply.go:366`, `internal/git/ops.go`
  (`StageUntracked`, `CheckDirtyRepo`, `BaselineUncommittedChanges`).
- Baseline setup: `internal/orchestrator/create/prepare_dirs.go:330`,
  `internal/git/ops.go:39` (`Baseline`); reset `internal/orchestrator/lifecycle/reset.go`.
- Precedent: Tart `SandboxSide` git exec; `ErrOverlayRequiresRuntime` (runtime-required diff).
- seatbelt residual: audit caps-F5 (permissive SBPL `mach-lookup`/`process-exec`).
- Audit finding C1 (2026-06-29, host-side execution injection), empirically reproduced.
