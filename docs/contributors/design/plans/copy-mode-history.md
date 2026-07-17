> **ABOUTME:** Design for making `:copy` preserve the source repo's real `.git` instead of
> stripping it to a fresh baseline. Backends lacking the host-git confinement invariant
> auto-degrade to a fresh baseline.

# Plan: preserve git history in copy mode

- **Status:** IN-PROGRESS — core landed (Linux/cross-platform) 2026-07-04, E2E-on-Docker
  verification still pending (D111). The default-preserve + `copy-strict` (suffix/flag/config) +
  `GitRunsInConfinement` gate + the `.git` CoW-clone all landed with unit tests; create/reset/diff/apply against a preserved `.git`
  still needs verification on a real Docker/Podman daemon (the dev sandbox has none). This design
  assumes the invariant that *every* sensitive work-copy git operation runs inside the sandbox's
  confinement. That invariant holds today only for docker/podman/containerd/tart; apple and
  seatbelt still run work-copy git host-side (a pre-existing RCE surface — see the companion
  plan **[confine-host-side-git.md](confine-host-side-git.md)**), so this feature **auto-degrades
  to `copy-strict` (with a one-time notice) on those backends** until they satisfy the invariant
  (see "Backend gating").
- **Depends on:** confine-host-side-git.md

Related: [copy-mode-git-rce.md](../../archive/plans/copy-mode-git-rce.md) (why work-copy git must run
in-confinement; the filter/textconv/fsmonitor RCE class), [config.md](../config.md) (the
three config surfaces), [security.md](../security.md).

## The problem

Under the default `:copy` mode, **the sandbox has no git history**. A copy of a repo with
thousands of commits shows a single synthetic `yoloai baseline` root commit, so the agent
(and user) cannot `git log`, `git blame`, `git bisect`, or `git log -S` inside the sandbox.

This is not a bug in the baseline logic — it is a side effect of how `:copy` copies:

- `:copy` (default, `includeIgnored == false`) honors `.gitignore` by enumerating the
  project through git (`git.ListProjectFiles` → tracked + untracked-not-ignored) and copying
  *that file list* (`copyFileList`, `internal/workspace/copy_gitignore.go:48`). Git's file
  enumeration never lists `.git` itself, so **`.git` is not copied**.
- With no `.git` in the work copy, `createCopyBaseline` sees `IsGitRepo == false` and takes
  the `g.Baseline()` branch (`internal/orchestrator/create/prepare_dirs.go:319-326`), which
  runs `git init` + `git commit -m "yoloai baseline"` (`internal/git/ops.go:40-46`). Fresh
  repo, no history.
- `:copy-all` (`includeIgnored == true`) uses `CopyDir` (`copy_faithful.go`), which *does*
  copy `.git`, so it hits `createBaselineForGitRepo` (record HEAD as baseline) and preserves
  history.

So two in-tree comments are **stale for the default mode** and must be fixed as part of this
work: `prepare_dirs.go:315` ("Docker: preserve original git history…") and
`copy-mode-git-rce.md:32` ("the project's real history — deliberately preserved") both
describe the `:copy-all`/faithful path, not the default.

### Why not just tell people to use `:copy-all`

`:copy-all` is the only way to get history today, but it forces two regressions at once:

| | default `:copy` | `:copy-all` | **this design** |
|---|---|---|---|
| gitignored live secrets kept out (`.env`, `*.pem`, `.aws/`) | ✅ | ❌ copies them | ✅ |
| git history available (`log`/`blame`/`bisect`) | ❌ | ✅ | ✅ |
| legitimate filters (LFS, git-crypt) run correctly | n/a | ✅ (in-confinement) | ✅ |
| agent-writable `.git` is an RCE surface | mitigated only where git is confined | mitigated only where git is confined | mitigated only where git is confined |

`:copy-all` reintroduces the gitignored-secret leak just to obtain history. This design gets
history **without** that regression: keep stripping never-committed gitignored files, but
preserve the real `.git`.

## Why preserving the real `.git` is now the right default

The historical reason to avoid a writable real `.git` in the work copy was the RCE class in
[copy-mode-git-rce.md](../../archive/plans/copy-mode-git-rce.md): host-side git ran `add`/`diff`/`status` against
the agent-controlled work copy, so a planted `filter.*.clean` / `diff.*.textconv` /
`core.fsmonitor` driver executed **on the host**. The 2026-06-29 fix moved those operations
**in-confinement** for docker/podman/containerd (and tart was always in-VM). Once git only
touches that `.git` from inside the already-compromised sandbox, the agent controlling
`.git/config` buys nothing new.

Given that, preserving the real `.git` is strictly better than a fresh baseline **on
confined backends**:

- **History** — `log`/`blame`/`bisect`/`log -S` work, which is a recurring need during real
  tasks (the whole reason this doc exists).
- **Legitimate filters run correctly.** This is the flip side of why Approach A was rejected
  in the RCE doc: a clean/stripped git setup silently breaks Git LFS (pointer vs. raw blob),
  git-crypt (plaintext leak), keyword expansion, and custom `textconv`. Real commits that
  diff and apply correctly need the real config, and the filters must run — which is safe
  precisely because they now run in-confinement.
- **Real commits** the agent makes carry forward through `format-patch`/`ApplySeries` with
  correct filter behavior.

### Secrets in history: the exception, not the default

The remaining objection to preserving history is that history can contain secrets. Quantify
the *marginal* exposure preserving `.git` actually adds:

- **Currently-tracked** secrets are already in the work copy today (`:copy` copies all
  tracked files). Preserving history adds nothing here.
- **Never-committed gitignored** live secrets (`.env`, `*.pem`) stay stripped either way —
  that hygiene is orthogonal to `.git` and is retained.
- The *only* new exposure is secrets **committed then removed** in a later commit — present
  in history, absent from HEAD.

That last set is exactly the "already compromised, rotate it" case: anything committed to a
repo's history is in every clone, fork, and backup; a sandbox stripping it from one copy is
security theater for that threat. Designing the *default* around it over-indexes on the
exception. The right home for that concern is an explicit opt-out (`copy-strict`), not the
default — with the standing advice that secrets in history must be rotated regardless.

## Design

### 1. Default `:copy` preserves the real `.git`

At copy time, in addition to the gitignore-filtered working-tree file set, **CoW/reflink-clone
the source `.git` into the work copy** (`internal/workspace` already has reflink/`clonefile`
fast-paths — `copy_faithful.go`, `copy_darwin.go`). Then `createCopyBaseline` sees a real
repo and records HEAD as the baseline (`createBaselineForGitRepo`, unchanged). The
gitignore-filtered working tree still matches HEAD for tracked files, and gitignored files
are untracked, so the baseline is clean.

Keep excluding never-committed gitignored files from the working tree — unchanged, orthogonal,
high-value. The change is narrowly: *also bring `.git`*, and let the existing
"record HEAD as baseline" branch fire instead of the fresh-init branch.

### 2. `copy-strict`: the opt-out (and the auto-fallback)

`copy-strict` = today's behavior: strip `.git`, fresh `yoloai baseline`, no history. It is
for two cases:

- **User opt-in:** secrets in history that haven't been rotated yet, or a deliberate
  minimal-provenance posture.
- **Automatic fallback:** any backend that does not satisfy the in-confinement invariant
  (apple/seatbelt today) auto-degrades to strict, *non-silently* (see gating).

Per the config-parity principle, `copy-strict` is exposed on **all three surfaces**:

- **Dir suffix:** `.:copy-strict` (and per aux dir, `~/other:copy-strict`). Composes as a
  copy variant alongside `:copy` / `:copy-all`.
- **Global flag:** `--copy-strict` (applies to the workdir and any `:copy` aux dirs that
  don't override with their own suffix).
- **Profile config default:** a `copy_strict: true` key in the profile `config.yaml`, so a
  security-conscious profile can make strict the default without per-invocation flags.

Precedence follows the existing config model (explicit dir suffix > flag > profile default),
mirroring how `:copy`/`:copy-all` and other per-dir modes already resolve.

### 3. Backend gating (the invariant)

History preservation is active **only when `runtime.GitRunsInConfinement(rt)` is true**
(`runtime/runtime.go:312` — `FilesystemLocality == LocalitySandboxSide || GitExecInConfinement`).
That is the single, already-existing predicate that says "sensitive work-copy git runs where
the agent already is." When it is false, preserving a writable real `.git` would *widen* the
host-side RCE surface (host-side `diff`/`status` firing agent-planted filters), so the feature
must not engage.

| Backend | `GitRunsInConfinement` | History preserved by default |
|---|---|---|
| docker / podman / containerd | ✅ (`GitExecInConfinement`) | ✅ |
| tart | ✅ (`LocalitySandboxSide`) | ✅ |
| apple | ❌ today → ✅ after confine-host-side-git | strict until fixed, then ✅ |
| seatbelt | ❌ today → ✅ after confine-host-side-git | strict until fixed, then ✅ |

**Auto-degrade is non-silent** — this is the failure mode that hid the missing history from us
for many turns and must not recur:

- Emit a one-line notice at create: *"apple/seatbelt: git history not preserved (work-copy git
  not yet confined); using copy-strict. See confine-host-side-git."*
- Surface it as a capability in `yoloai info` (like an unsupported netpolicy strategy).

Once [confine-host-side-git.md](confine-host-side-git.md) makes apple/seatbelt satisfy the
invariant, they preserve history with no further change here — the gate flips automatically.

### 4. Cost / size

Git objects are immutable, so a **reflink/`clonefile` clone of `.git` shares blocks and is
near-free** on CoW filesystems (APFS, Btrfs, XFS-reflink, overlay-on-reflink) — the "huge
`.git`" concern only materializes on non-CoW filesystems, where the clone is a real byte copy.
Mitigations for that case: `copy-strict` (no `.git` at all), or a future depth/`--since`-limited
history import (out of scope here; noted as a follow-up). On CoW filesystems, preserving full
history is effectively free.

## Costs / behavior changes (honest)

- **`git add`/`commit` inside the sandbox now run the repo's real filters** (in-confinement) —
  correct, but slower, and dependent on the profile image having the filter tools (`git-lfs`,
  git-crypt, etc.). A missing tool breaks commit/diff the same way `:copy-all` does today; this
  is an image-provisioning consideration, not new to this design.
- **Submodules:** the main `.git` is cloned, but submodule *working trees* are not checked out
  (existing `:copy` limitation — `copyFileList` skips gitlink dirs). `git log` on the
  superproject works; submodule blame/log does not. Documented limitation, unchanged.
- **A linked worktree as the workdir gets no history at all** — not a clone of a smaller `.git`,
  none. Its `.git` is a pointer file and its objects live in the main repo's common dir, outside
  the copied tree, so there is nothing here to clone; the work copy is severed from the link and
  given a fresh baseline, and create warns that history was not preserved. Keeping the pointer
  instead is what DF116 was: on the host it resolves, and the baseline commit lands in the user's
  real repo. Preserving worktree history properly needs the common dir and is a separate design —
  [worktree-history.md](worktree-history.md). The same applies to a submodule directory named
  directly as the workdir, for the same reason.
- **Non-CoW filesystems** pay a real `.git` copy at create (see §4).
- The apply path is unchanged: `ApplySeries`/`ApplyPatch` still run host git via
  `git.NewHost` against the user's **own** (user-controlled, not agent-controlled) real repo,
  hooks disabled — same trust level as the user running git themselves.

## Testing

- Copy-mode create on a real git repo (container backend): assert `git log` in the sandbox
  shows real history and the baseline equals source HEAD (not a `yoloai baseline` root).
- A filtered repo (Git LFS / a `filter.redact.clean`): assert in-sandbox commit + diff/apply
  round-trips correctly (the Approach-A corruption case must NOT reproduce).
- `copy-strict` via each of the three surfaces: assert `.git` stripped, fresh baseline.
- Gating: on an unconfined backend (apple/seatbelt), assert auto-strict + the notice fires and
  `yoloai info` reports history-not-preserved.
- Secret-in-history opt-out: assert `copy-strict` yields no history objects.

`make check` does not exercise real backends or macOS; container-backend behavior is verified
on real Docker/Podman, and apple/seatbelt gating is verified on real macOS hardware (as with
other backend work).

## Decisions to record

This plan should get a decision entry in `decisions/working-notes.md` capturing: (a) default
`:copy` preserves real `.git` gated on `GitRunsInConfinement`; (b) `copy-strict` is the opt-out
+ auto-fallback, on all three config surfaces; (c) the secrets-in-history rationale (marginal
exposure = committed-then-removed = rotate-anyway). Cross-reference the RCE doc and
confine-host-side-git.

## Open items

- CLI naming: `copy-strict` suffix + `--copy-strict` flag + `copy_strict` config key — confirm
  the exact spellings against existing suffix/flag conventions in `config.md`/`commands.md`.
- Depth/`--since`-limited history import for non-CoW filesystems — deferred; capture as a
  follow-up if the full-`.git` copy proves costly in practice.
