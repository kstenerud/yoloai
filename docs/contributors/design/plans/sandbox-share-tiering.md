> **ABOUTME:** Design for closing DF136/DF148 at the root by tiering the sandbox directory into
> host-only / guest-read-only / guest-read-write regions, so host-only metadata is never in a
> guest-writable share — converging tart and seatbelt on the invariant Docker already upholds.

# Sandbox directory share tiering (host-only / read-only / read-write)

- **Status:** PLANNED — design draft from the DF136 solution-audit conversation (2026-07-20); not
  yet a locked decision, not implemented. The interim (§ Interim) is shippable independently; the
  full tiering is the target end state. Empirical facts below are marked confirmed-on-hardware.
- **Depends on:** —

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

**Seatbelt** — file-granular (SBPL), so it can express all three tiers directly. Two options:
- *Allowlist (target):* replace the broad `(allow file-read* file-write* (subpath sandboxDir))` with
  explicit per-item rules — read-write on the read-write tier, read-only on the read-only tier, and
  *no rule* for the host-only tier (default-deny covers it). Safe-by-default: a new host-only file is
  protected automatically. Cost: enumerate exactly what the agent needs; a miss fails closed and
  loudly at runtime.
- *Denylist (interim):* keep the broad grant, append `(deny file-write* (literal <env.json>))` and
  one for `sandbox-state.json`. See § Interim.

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

## Interim: the seatbelt deny (shippable now, superseded by the allowlist)

Before the full reorg, the seatbelt half of DF136 can be closed in ~2 lines: append, **after** the
existing broad allow, `(deny file-write* (literal <sandboxDir/environment.json>))` and the same for
`sandbox-state.json`. **Confirmed on macOS Tahoe seatbelt (2026-07-20):**
- Rule precedence is **last-match-wins**: the later `deny` overrides the earlier `allow (subpath …)`
  for that path; other files in the dir stay writable.
- `file-write*` on the literal path covers **write, unlink, and rename-over** — delete-and-recreate
  and `mv`-over-it are both blocked, so there is no obvious bypass of the single-file deny.

This is prevention (strictly stronger than signing — the agent cannot even corrupt the file), but it
is a *denylist*: every future host-only file needs its own line, which is why it is the interim and
the allowlist is the target. It does **not** help tart (no file-granular exclusion), so tart waits
for the reorg.

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
- Latent hardening: the `bin/` setup/monitor scripts the guest currently *execs* become
  read-only too, so they cannot be rewritten from inside the sandbox.

## Open questions

- **Seatbelt: allowlist vs. keep-the-deny.** The allowlist is safe-by-default but risks missing an
  access the agent needs (fails closed, but noisily). Decide whether to pay the enumeration cost or
  keep a maintained denylist of host-only files.
- **Tart: subdir reorg vs. relocate-metadata-only.** The `rw/`+`ro/` reorg is the clean convergence;
  a lighter variant relocates only `environment.json`/`sandbox-state.json` to a sibling host-only
  dir and leaves the rest shared read-write (smaller change, but leaves `runtime-config.json` and
  the `bin/` scripts guest-writable — i.e. does not close DF148). Choose based on whether DF148 is
  in scope for the same effort.
- **Migration shape.** In-place move of existing sandbox dirs vs. dual-shape readers with a
  settling period. Ties into the deprecation register entry.

## Related

- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — a different
  hostile-agent surface (in-container sudo flushing the firewall); shares the "don't trust the
  agent's own environment" spirit but no code overlap.
- [apply-drift-guard.md](apply-drift-guard.md) — the other half of the `apply` trust surface (the
  review-to-apply gap); orthogonal to the metadata-integrity problem here.
