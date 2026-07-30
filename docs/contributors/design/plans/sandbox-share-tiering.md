> **ABOUTME:** Design for closing DF136/DF148 at the root by tiering the sandbox directory into
> host-only / guest-read-only / guest-read-write regions, so host-only metadata is never in a
> guest-writable share — converging tart and seatbelt on the invariant Docker already upholds.

# Sandbox directory share tiering (host-only / read-only / read-write)

- **Status:** IN-PROGRESS — design confirmed with the owner 2026-07-20; being built on branch
  `sandbox-share-tiering`. The concrete design is in § Confirmed design below; the earlier prose
  stands as the reasoning that led there. Empirical facts are marked confirmed-on-hardware.
- **Depends on:** —

## Confirmed design (2026-07-20)

The owner chose the fully principled fix over the cheap interim: make the tier a **physical
directory**, so classification is "which directory does this file live in", not a list that can
drift. The sandbox-dir root holds **only three subdirectories** — there is no un-tiered place to put
a new file, and one module becomes the sole path-builder, every helper rooted in a tier. That module
is `internal/config/sandbox_layout.go` rather than `store/paths.go` (see sequencing step 1 for why);
`store/paths.go` re-exports it.

```
<sandboxDir>/
  host/   — never shared to any guest
  ro/     — guest read-only
  rw/     — guest read-write
```

**File → tier** (from the host+guest surface maps):

| Tier | Contents |
| --- | --- |
| `host/` | `environment.json`, `sandbox-state.json`, `agent.json`, `netpolicy.json`, `backend/` (SBPL profile, pids, VM/CNI state), `network-diag.txt` |
| `ro/` | `runtime-config.json`, `bin/` (guest-exec'd scripts), `prompt.txt`/`resume-prompt.txt`, `machine-id`, `home-seed/`, `secrets/` (ephemeral, guest-read) |
| `rw/` | `logs/` (+ the `.secrets-consumed`/`.substrate-ready` markers), `agent-runtime/`, `agent-status.json`, `files/`, `cache/`, `home/`, `work/`, `tmux/`, `setup.log`, `vscode-cli/`, the create-done marker |

**The guest sees one flat root; its scripts are unchanged.** The in-sandbox scripts build every path
as `os.path.join(yoloai_dir, "logs", …)` from a single root, so each backend assembles that flat
root as a *view* over the tiers. Not touching the guest scripts is itself drift-elimination (they are
the same bytes on every backend; ~20 fragile path-joins stay untouched).

**Per-backend realization:**
- **docker/podman/containerd/apple** — already bind per-file at the right RO/RW and never mount the
  root; just repoint each MountSpec HostPath to its tier location. `host/` stays invisible for free.
- **tart** — two VirtioFS shares (`ro`, `rw`) replace the one whole-root share; the existing symlink
  tree assembles the flat guest view; `host/` gets no `--dir`, so it is unreachable.
- **seatbelt** — grant read+write on `rw/`, read on `ro/` and the view dir, nothing on `host/`;
  `yoloai_dir` becomes the view dir. **Confirmed on macOS Tahoe (2026-07-20):** writing through a
  view symlink is enforced at the *target's* tier — a write via the view to `rw/` lands, a write via
  the view to `ro/` is blocked, and a write to ungranted `host/` (the DF136 attack) is blocked.

**Six files the table above missed** (found 2026-07-27 by funnelling every ad-hoc path joiner
through `internal/config/sandbox_layout.go` — the table was written from the host+guest *access*
maps, which never enumerated these). Each is classified from what the code already says about it,
but none has been reviewed against the design:

| File | Tier | Evidence |
| --- | --- | --- |
| `injector.json` (pid/addr record) | `host/` | host-side injector bookkeeping; guest never reads it |
| `injector.log` | `host/` | written by the host-side injector process |
| `injector-token` | `host/` | `internal/broker` states it is "never bind-mounted", which is what stops a co-resident container learning another sandbox's token |
| `context.md` | `ro/` | seeded by the host, read by the agent |
| `log.txt` (containerd) | `rw/` | `runtime/containerd/logs.go` calls it a "bind-mounted file" the guest writes |
| `lifecycle-on-create-done` | `rw/` | the create-done marker the table already lists under `rw` |

The lesson generalizes: a classification table built by enumerating *accesses* misses any file
whose access nobody happened to describe. The path-builder funnel enumerates by *construction*
instead, so it cannot miss one — which is the argument for keeping the funnel as the gate rather
than the table.

**And a seventh entry that is not a missed file but a new producer** (found 2026-07-30 rebasing
onto main). DF156's remedy (c) landed `deliverRuntimeScripts`
(`internal/orchestrator/launch/launch.go`), which writes the runtime scripts into `bin/` **on every
launch** and returns its own read-only `MountSpec` for them. `bin/` was already classified `ro/`, so
the tier is unchanged and host-written/guest-read is the same shape as `runtime-config.json`. What
is new is a *fourth* mount spec for step 3 to repoint, on a path that did not exist when the
sequencing was written. Note the failure mode this exposes: the funnel is a gate only against call
sites that exist when it is built — a branch that funnels and then waits accumulates new ad-hoc
joiners on main, invisibly, because they conflict with nothing. This one arrived that way
(`filepath.Join(st.SandboxDir, config.BinDirName)`, rebased clean, repaired to `config.BinPath`).
Re-run the funnel grep after every rebase; it is the only thing that finds them.

**Two judgment calls (owner-approved):**
- `tmux/` → `rw` wholesale: it holds a runtime-created socket (needs write); `tmux.conf` rides along
  writable — low-risk, the agent's own multiplexer running as the agent, no privilege boundary.
- the seatbelt process-log moves from `backend/` to `rw/logs/`, so `backend/` is cleanly host-only.

**Migration:** existing sandboxes are flat → schema bump **v5→v6** with a `TierLayout` migrator
following the v3→v4 overlay-flatten precedent (scratch on the same filesystem, atomic promotion,
stamp written **last** per D110). Register the migration in [deprecations.md](../../deprecations.md).

**Sequencing (reviewable commits on the branch):**
1. Prep-refactor: funnel every ad-hoc per-sandbox path joiner through one builder (no behavior
   change) so the later move is one-place. Done in two passes: `runtime-config.json` first, then
   (2026-07-27) the remaining ~55 across 25 files. The builders live in
   `internal/config/sandbox_layout.go`, not `store/paths.go`, because the runtime backends,
   `internal/broker` and `internal/netpolicycfg` cannot import `store` — and `internal/cli` is
   forbidden from importing it by depguard. `store/paths.go` re-exports them for its own callers.
2. The tier roots + every helper rooted in its tier; the four single-joiner metadata files
   (`environment/sandbox-state/agent/netpolicy`) into `host/`. **Landed 2026-07-27 for the four
   host-tier files only** — `ro/` and `rw/` are still flat, because moving those changes what the
   backends mount and that is step 3's job. Note the resulting window: from this commit until the
   step-4 migrator lands, **an existing sandbox reads as missing**, since its records are still at
   the flat paths. `config.EnsureHostTier` makes each host-tier writer create its own tier, so
   nothing depends on the creator having made it.
2b. **The seatbelt host-tier deny — landed 2026-07-30.** One `(deny file-write* (subpath …/host))`,
   emitted last, kernel-verified. Closes the seatbelt half of DF136 now rather than at step 3, and
   is a permanent backstop rather than a stopgap; see § The seatbelt host-tier deny.
3. `create.go` dir creation + the backend wiring (`mounts.Build`, tart two-share + view, seatbelt
   per-tier grants + view). **Seatbelt's `ro/` grant carries a `(deny file-write* (subpath …/ro))`
   backstop on the same pattern** (owner, 2026-07-30). This is not belt-and-braces for its own sake:
   seatbelt expresses read-only as the *absence* of a write grant, so it holds only while nothing
   else grants write over that path — measured, DF161, where the profile's own broad temp grant
   silently defeated a read-only mount. Tart needs no equivalent; its `:ro` VirtioFS share is a real
   read-only mount and is unconditional. The two backends are converging on one invariant by
   different mechanisms, and only one of them is self-enforcing.
4. The v6 `TierLayout` migrator.
5. **The guarantee is a conformance case, not per-backend tests.** The `runtimetest` mount section
   now runs on all six backends (DF161, landed 2026-07-30), so the tier invariant has a home no
   fake can satisfy: assert *from inside the guest* that `host/` is unreachable and that `ro/` is
   readable and not writable. Before writing it, name the backends that reach the assertion and
   confirm the intersection is non-empty (rule 10, A15) — a capability-guarded check whose guard
   nothing passes is six green vacuous tests. Then DF136 and DF148 drain: DF136's seatbelt half
   already has (step 2b), its tart half waits on step 3. The invariant itself graduates into
   `architecture/host-layout.md`; a rule that lives only in a resolved finding is unreachable
   (D128 (3), DF151).

## Problem

On tart and seatbelt the **entire** sandbox directory is exposed to the in-sandbox agent as one
coarse read-write region — tart shares it as a single read-write VirtioFS mount
(`runtime/tart/tart.go:708`, `--dir yoloai:<sandboxPath>`); seatbelt grants read-write over the whole
subpath (`runtime/seatbelt/profile.go:193-199`). The sandbox dir holds host-only metadata
(`environment.json`, `sandbox-state.json`) alongside the things the guest genuinely needs, so the
agent can rewrite records it should never touch. That is **DF136** (rewrite `environment.json`'s
`HostPath` → redirect a host-side `apply` to any path the user can write) and **DF148**
(`runtime-config.json` is guest-writable and read back by the host post-launch).

The write-access is **incidental, not intended.** Nothing in any guest reads or writes
`environment.json` or `sandbox-state.json` (confirmed by grep across all Go and guest Python). They
are collateral of a whole-directory grant that exists because *other* things in the directory must
be guest-reachable.

**Docker already does this correctly.** It never bind-mounts the sandbox-dir root; it binds each
needed item individually (`internal/orchestrator/mounts/mounts.go:186-233`) — `logs/`, `files/`,
`cache/`, `agent-runtime/` as directory binds, and `runtime-config.json` (**read-only**),
`agent-status.json` (read-write), `prompt.txt` (read-only) as single-file binds. The two host-only
metadata files are never named, so they are absent from the container. tart and seatbelt are the
outliers.

## The invariant

> **Host-only metadata is never inside a guest-visible region. The guest sees only the specific
> subtrees it needs, each mounted at the narrowest access it requires.**

This is exactly Docker's rule ("never mount the root; expose named items at named modes"). The plan
makes tart and seatbelt uphold it too — by **prevention** (the agent cannot write what it cannot
reach), not detection.

## The three tiers

Every item under the sandbox dir sorts into one of three tiers. Classification from the guest
read/write enumeration (DF136 audit):

| Tier | Guest access | Items |
| --- | --- | --- |
| **host-only** | none (never shared) | `environment.json`, `sandbox-state.json` |
| **read-only** | read | `runtime-config.json`, `bin/` (setup/monitor scripts), `tmux/tmux.conf`, `prompt.txt` |
| **read-write** | read + write | `logs/`, `agent-runtime/`, `agent-status.json`, `setup.log` (tart), the work copy |

Notes:
- The **work copy** is guest-read-write but is already handled outside the sandbox-dir share on
  seatbelt (copy-mode work copy is a separate host path with its own tight git sub-profile,
  `runtime/seatbelt/profile.go:44-48`) and via symlink/mount on tart — it is not a new concern here.
- `runtime-config.json` is host-**written** (the host patches `working_dir`/`mount_map` before
  launch, `runtime/tart/tart.go:931`, `runtime/seatbelt/seatbelt.go:854`) but guest-**read** — the
  read-only classification is about *guest* access; the host always has full access to the real
  files on disk regardless of how they are shared.
- `agent-status.json` and `setup.log` are guest-**written**, so they are read-write even though they
  sit at the sandbox-dir root today — the tiering cleaves root-level files, not just subdirectories.

## Per-backend realization

**Docker** — already correct. No change; it is the reference the other two converge on.

**Seatbelt** — file-granular (SBPL), so it can express all three tiers. **Decided (owner,
2026-07-30): the allowlist *and* an explicit deny backstop — not one or the other.**
- *Allowlist:* replace the broad `(allow file-read* file-write* (subpath sandboxDir))` with explicit
  per-tier rules. That grant has to actually **go**, not be supplemented: it is DF136 itself, and the
  `ro/` tier cannot mean anything while it stands.
- *Deny backstop:* `ro/` and `host/` each carry `(deny file-write* (subpath …))` in the trailing
  block. **This is not belt-and-braces, and the reason corrects what this section used to say.** It
  claimed a host-only tier needs *no rule* because "default-deny covers it", and that a new
  host-only file would therefore be "protected automatically". **That is false on seatbelt, and was
  measured false.** Absence of a grant is not a denial — it holds only while nothing *else* grants
  write, and this profile grants write broadly: the temp tree, the Xcode and SwiftPM caches, the
  sandbox dir, any enclosing read-write mount. A `:ro` mount under any of them was silently writable
  until 2026-07-30 (DF162). On seatbelt every negative permission must be **stated**, positioned
  last, and ordered by specificity.
- Status: the `host/` deny is landed (§ The seatbelt host-tier deny); `ro/`'s lands with step 3.
- Contrast tart, which needs no equivalent: its `:ro` VirtioFS share is a real read-only mount and
  holds unconditionally. The two backends converge on one invariant by different mechanisms, and
  only one of them is self-enforcing — so "grant read on `ro/`" is a weaker sentence here than the
  identical words are there.

**Tart** — directory-granular (VirtioFS shares a whole directory subtree; a single file cannot be
excluded). Realize the tiers as **directory** shares, which tart already supports:
- Do **not** share `<sandboxPath>` itself. The host-only tier is simply the unshared root, where
  `environment.json`/`sandbox-state.json` stay.
- Share `<sandboxPath>/rw` read-write (`--dir yoloai-rw:<sandboxPath>/rw`).
- Share `<sandboxPath>/ro` read-only (`--dir yoloai-ro:<sandboxPath>/ro:ro`) — tart already emits
  `:ro` shares for Xcode (`runtime/tart/tart.go:758-767` appends `:ro` for `ReadOnly` mounts), so
  this needs no new primitive.

This requires moving the guest-facing content under `rw/` and `ro/` subdirs and updating the guest
mount points and the tart symlink wiring (`runtime/tart/mounts.go`). It is a layout change, but it
uses only existing sharing primitives.

## The seatbelt host-tier deny — landed 2026-07-30, and no longer an interim

This section used to describe a stopgap: `(deny file-write* (literal <sandboxDir/environment.json>))`
plus one line per host-only file, kept second-best because *"every future host-only file needs its
own line"*. **That objection died when the tier became a directory.** One
`(deny file-write* (subpath <sandboxDir>/host))` covers every host-only record that exists now and
every one added later, with nothing to maintain — so the deny is not a weaker alternative to the
allowlist, it is the **backstop the allowlist should keep**. Implemented in
`writeProfileHostTierDeny` (`runtime/seatbelt/profile.go`).

Two properties make it work, and both are ways it could have read as correct while doing nothing:

- **Position.** It is emitted *last*. SBPL is last-match-wins, and the profile grants write over the
  whole sandbox dir, and over the temp tree that every test's sandbox dir lives inside. A deny
  written before either is dead text.
- **Spelling.** The variants are resolved from `sandboxDir` (which always exists) and the tier name
  appended, not resolved from the tier dir itself — `resolvePathVariants` uses `EvalSymlinks`, which
  fails on a path that does not exist yet, and the tier is created lazily. Resolving the parent
  yields both the `/var/…` and `/private/var/…` spellings whether or not the directory is there, so
  a write through the unresolved spelling cannot walk past a deny written only for the resolved one.

**Verified against the kernel, not against the profile text** (macOS, 2026-07-30):
`TestSeatbelt_HostTierIsUnwritableFromInside` boots a real sandbox-exec'd process, fails its write
into `host/`, and confirms the record is byte-identical afterwards — while a write elsewhere in the
sandbox dir still succeeds, so the deny is scoped rather than blanket. With the rule removed the test
fails and the record on disk reads `tampered`, i.e. it reproduces DF136 exactly.

**Still open, and not addressed by this:** tart has no file-granular exclusion, so tart's half of
DF136 waits for the step-3 reorg; and `ro/` has no equivalent backstop yet because the tier does not
exist yet (see sequencing step 3). Earlier framing of the deny as inferior-to-allowlist is retained
above deliberately — the reasoning was right for a flat layout and wrong for a tiered one, and that
distinction is the transferable part.

## Migration

Existing on-disk sandboxes have the flat layout. Moving to `rw/`+`ro/` subdirs (tart) or changing
the seatbelt grant shape means either migrating extant sandbox dirs or teaching the readers both
shapes. A migration **is** a deprecation — register it in
[deprecations.md](../../deprecations.md) with the date incurred (rule 9) when this lands. The
path helpers in `store/paths.go` (which construct every per-sandbox subpath) are the single choke
point for the new layout, so most of the change is localized there plus the two backends' mount
wiring.

## Why not the alternatives (recap of the audit)

- **Compare the apply target against a recorded path** — *unsound*. Every in-tree anchor for the
  comparison (`environment.json` itself, the caret-encoded `work/<EncodePath(hostPath)>` dir name)
  is *also* inside the agent-writable tree and can be forged in lockstep. Killed in the DF136 entry.
- **Sign/verify the record** — viable and uniform across all backends with no layout migration, but
  it is *detection, not prevention* (the agent can still corrupt the file → sandbox won't load), and
  it adds a host-only key, a whole-record signature scope, and a signing migration for existing
  records. Tiering supersedes it as the primary control: if the metadata is genuinely not in a
  guest-writable place, there is nothing to forge. Signing drops to optional belt-and-suspenders
  against a *future* accidental re-widening of a share.

## What this closes

- **DF136** — `environment.json` moves to the host-only tier; the redirect primitive is removed on
  all backends.
- **DF148** — `runtime-config.json` moves to the read-only tier (matching Docker); the guest can no
  longer rewrite a file the host reads back.
**Not a third thing it closes, and the plan said otherwise until 2026-07-30.** An earlier bullet
here claimed "latent hardening: the `bin/` scripts the guest execs become read-only, so they cannot
be rewritten from inside the sandbox." **That is not a security benefit and must not be cited as
one.** The scripts run as the agent's own principal, so anything the agent gains by rewriting one it
gains by killing the process and running its own copy; read-only raises the effort by nothing. The
general test, and the reason the other two bullets survive it: a read-only grant is a real control
only where the agent can write a file that a **more privileged** principal later executes or trusts.
For `bin/` that set was enumerated and found empty (DF156, resolved). For `environment.json` and
`runtime-config.json` it is non-empty and the privileged reader is the **host** — which is exactly
why DF136 and DF148 are real and this was not. Recorded rather than deleted because the inference is
re-derivable from the permissions alone: A16 is the record of it being made and retracted, and it
was made a second time, from this bullet, on 2026-07-30.

Keep `bin/` in `ro/` regardless — a guest that never writes it should not have write access, and
tier assignment follows access, not threat model. Just do not bank a security claim on it.

## Open questions

- **Migration shape.** In-place move of existing sandbox dirs vs. dual-shape readers with a settling
  period. Ties into the deprecation register entry (rule 9). **The only genuinely open one**, and it
  is now the gate on merging: since step 2 an existing sandbox reads as missing, and no test anywhere
  covers an upgrade — `scripts/smoke_test.py` has no schema, migration or pre-existing-sandbox case,
  and `releasetest` creates fresh sandboxes, so a green release gate says nothing about it.
- **The seven late-classified entries** (§ Confirmed design) are still classified from what the code
  says about them, and still not reviewed against the design. Cheap, and it feeds step 3.

**Answered, kept because the reasoning is the transferable part:**

- ~~Seatbelt: allowlist vs. keep-the-deny.~~ **Both** (owner, 2026-07-30). Framing them as
  alternatives was the error: the allowlist replaces the broad grant, the deny makes the result
  non-defeasible. See § Per-backend realization for why "default-deny covers it" does not hold here.
- ~~Tart: subdir reorg vs. relocate-metadata-only.~~ **The reorg**, settled in § Confirmed design —
  the light variant leaves `runtime-config.json` and `bin/` guest-writable and so does not close
  DF148, which is in scope.

## Related

- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — a different
  hostile-agent surface (in-container sudo flushing the firewall); shares the "don't trust the
  agent's own environment" spirit but no code overlap.
- [apply-drift-guard.md](apply-drift-guard.md) — the other half of the `apply` trust surface (the
  review-to-apply gap); orthogonal to the metadata-integrity problem here.
