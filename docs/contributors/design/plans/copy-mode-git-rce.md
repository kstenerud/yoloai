# Plan: close the copy-mode host-git RCE (audit C1, CRITICAL)

ABOUTME: Design for neutralizing host code execution via git filter/diff/fsmonitor
ABOUTME: drivers in the agent-controlled copy-mode work-copy .git — the diff/apply path.

Status: **design only, not yet implemented.** Surfaced by the 2026-06-29
escape/exfil security audit (finding C1, CRITICAL, empirically reproduced). The
v0.6.0 tag should wait for this fix (it is the only CRITICAL finding).

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

Worst on **Seatbelt** (the work copy is on the host FS and host git runs *outside*
the macOS sandbox = full escape). Tart is SandboxSide (git runs in-VM — not a host
path). The overlay diff/apply paths exec in-container — also not host. The exposure
is exclusively the host `hostExec` copy-mode path (docker / podman / containerd /
seatbelt — the `HostSide` backends).

The **sandbox→host apply** path (`git apply`/`git am` into the user's real target
repo, `internal/git/ops.go`) is **not** affected: that repo's `.git/config` is
user-owned (the agent can't define drivers there), hooks are killed, and
`--unsafe-paths` is gated. Confirmed sound by the audit.

## Why it is not a one-liner

The obvious fix — "don't read the agent's `.git/config`" — collides with the fact
that copy mode deliberately keeps the project's real git history in that `.git`
(for the agent's `git log/blame`, and the baseline SHA is the original HEAD). The
host needs those original objects to diff against the baseline, so it cannot simply
delete or empty the work-copy `.git`. Git always reads config from whatever
`--git-dir` it is pointed at; there is no flag to read objects from a git-dir while
ignoring that git-dir's config, and no flag to ignore an in-tree `.gitattributes`.
Defense-in-depth flags (`--no-ext-diff --no-textconv -c core.fsmonitor=`) kill the
diff and fsmonitor vectors but **not** the `git add` clean-filter (verified), so
they are necessary-but-insufficient.

## Options

### A. host-private git-dir + shared objects via alternates (recommended)

Keep the agent's `workDir/.git` untouched (agent keeps full history). For yoloAI's
**own** host-side work-copy git (diff/apply/status), run
`git --git-dir=<private> --work-tree=<workDir>` where `<private>` is host-owned
(under the sandbox state dir, **not** bind-mounted) and has a **clean config**.

- No agent-defined filter/diff/fsmonitor command ever runs (clean config). The
  agent's `.gitattributes` is still read, but the drivers it names are undefined in
  the clean config, so they are inert. **Empirically verified:** with
  `--git-dir=<private> --work-tree=<work>`, a planted `filter.pwn.clean` did not
  fire and the top-level `.git` was correctly skipped by `git add -A`.
- `<private>` reaches the original commit objects (to resolve the baseline SHA and
  its tree) **without copying** them, via a one-line `objects/info/alternates` →
  `<workDir>/.git/objects`. Objects are SHA-addressed, so the agent cannot forge
  them; at most it can delete objects (a harmless diff error, not RCE).
- Set up once at baseline creation (after `baselineSHA` is determined): init
  `<private>`, write the alternates file, `update-ref` the baseline SHA, write a
  clean config.

**Preserves all current behavior:** the agent keeps git; you can still diff/apply a
**stopped** sandbox (pure host-side).

**Implementation surface:** a host-private git-dir path (store), baseline setup in
`prepare_dirs.go` + `reset.go`, the git execer for `NewSandbox`/`HostSide`
work-copy ops gains a "private git-dir + work-tree" mode (vs `-C workDir`), threaded
so only work-copy ops use it (the host **target** apply ops keep using the user's
repo directly). Add the defense-in-depth diff flags as belt-and-suspenders.

**Must verify on real Docker/Podman:** diff/apply produce byte-identical output to
today across binary diffs, `--stat`/`--numstat`/`--name-only`, path filters, the
`:copy` aux-dir pipeline, and the dirty-baseline (`BaselineUncommittedChanges`)
case. `make check` does not exercise real diff/apply.

### B. route copy-mode work-copy git in-container

Run the diff/apply/status git **inside** the sandbox container (the mechanism Tart
already uses as a `SandboxSide` backend), so a malicious filter executes inside the
already-compromised sandbox, never on the host. yoloAI harvests the textual diff.

- Simpler (no alternates / private git-dir); reuses `sandboxExec`.
- **Behavior change:** copy-mode diff/apply would then **require the sandbox
  running** (today a stopped copy-mode sandbox is diffable host-side), and each op
  costs a container exec. Also leaves baseline integrity in the agent-writable
  `.git` (the agent could rewrite the baseline to hide changes — a separate, lesser
  integrity concern, out of scope for the RCE).

## Recommendation

**A (host-private git-dir + alternates).** It closes all three vectors, preserves
every current behavior (agent git, stopped-sandbox diff, pure host-side), and the
core mechanism is already empirically validated. B is the fallback if the alternates
plumbing proves too invasive in the diff/apply layer.

## Pointers

- Sink: `internal/git/git.go:93` (`hostExec.run`, hooks-only neutralization).
- Triggers: `copyflow/diff.go:132`, `copyflow/apply.go:366`, `internal/git/ops.go`
  (`StageUntracked`, `CheckDirtyRepo`, `BaselineUncommittedChanges`).
- Baseline setup: `internal/orchestrator/create/prepare_dirs.go:330` (preserve-history),
  `internal/git/ops.go:39` (`Baseline`); reset: `internal/orchestrator/lifecycle/reset.go`.
- Dispatch: `internal/git/git.go` (`NewSandbox` / `hostExec` vs `sandboxExec`,
  `runtime.FilesystemLocality`).
- Audit finding C1 (2026-06-29, host-side execution injection), empirically reproduced.
