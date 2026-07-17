> **ABOUTME:** The scope decision list for v0.9.0 — what rides this release, what defers, and what
> is owed at the tag. An index of decisions with pointers, never a copy of what the findings say.

# Plan: v0.9.0 scope

- **Status:** IN-PROGRESS — assembled 2026-07-17. Decisions marked **OWNER** are open.
- **Depends on:** —

## Why this exists, and why it must die

Things get lost in the shuffle. Not, it turns out, mostly by being forgotten — by being *recorded in
a doc nothing keeps in step*, so the record outlives the work and nobody can tell which is stale
(DF103). Two of the three "surely this was owed from last release" candidates checked below had
already shipped; the docs claiming otherwise were just old.

**This file holds decisions and pointers. It does not describe anything.** Every row points at the
record that owns the detail — a `DF<n>`, a plan, a decision. Restating their content here would
create a fourth copy to drift (D121), which is the exact failure this file exists to survive.

**It moves to `archive/plans/` when v0.9.0 ships** (rule 8). A release-scope list that outlives its
release is a backlog, and a backlog is where work goes to be invisible.

## The economics — the only reason to force something in

v0.9.0 is **already breaking** (D126: instance names, the empty principal) and **already ships a
migration** (library schema v4→v5). That makes exactly one class cheap now and expensive forever:

- **Breaking** — users take one break either way. A second break later costs them twice, for the same
  prefix, with the same explanation.
- **Schema-touching** — `system migrate` runs once either way. A later change means schema v6, a
  second migrator, and a second migration a user must run.

**Anything neither breaking nor schema-touching has no reason to be here.** It can land any release.
Being valuable is not a reason; DF131 is the highest-value unbuilt gate in the repo and is correctly
*not* on this list.

## In — recommended

| Item | Why now | Record |
| --- | --- | --- |
| **Profile image tags carry no principal** | Breaking (a user-visible tag renames) **and** schema-touching (`ImageRef` is persisted, so a rename must re-stamp records). Latent, so it will never justify its own break — meaning it never gets done unless it rides one. This is the last cheap moment, and D126's own record says that reasoning is how the empty principal survived. | [DF126](../findings-unresolved.md) |
| **Init sentinel, P1 (record + diagnose only)** | Not breaking; included because it *resolves* DF128 rather than rewording it, and it is small. Drop it without argument if the release is tight — P2 (retry) is explicitly not proposed. | [init-sentinel.md](init-sentinel.md), [DF128](../findings-unresolved.md) |

## OWNER — undecided, and each is a real fork

Ordered by how much the "now or never" argument actually applies.

| Item | The call | Record |
| --- | --- | --- |
| **`--network-isolated` is IPv4-only; the capability claim doesn't say so** | User-visible either way: add v6 rules (expensive, per-backend) or disable IPv6 in the guest (the finding recommends this as cheaper — and it removes IPv6 from every sandbox, which is a behaviour change best sprung on a release users already expect to change). | [DF104](../findings-unresolved.md) |
| **tart silently drops `-p` port mappings** | Minimum viable: make tart *reject* `-p` instead of ignoring it. "Newly rejects input" is rule-1 breaking — cheap on this release, rude on a patch. Needs real macOS verification. | [DF53](../findings-unresolved.md) |
| **`destroy` frees the name while the instance survives; next `start` adopts it** | The remedy "touches the shipped `start` contract" and likely wants a provenance field in sandbox metadata — i.e. schema, while we are already migrating. Confirmed by reproduction. Also interacts with the v4→v5 rename. | [DF113](../findings-unresolved.md) |
| **Substrate store shape (`AgentType`/`Model`: opaque payload vs sidecar)** | Self-identifies as *"another versioned migration"* on `environment.json`. Phase A removed the forcing function, so the honest question is "do we still want it?" — but it has to be asked now, not after. | [module-split.md](module-split.md) §Open questions |
| **`keepalive_only` default flip + retire the legacy entrypoint** | A default flip and a removed launch path, plus a `runtime-config.json` reshape — whose `schema_version` is *written but not validated*, so a reshape breaks silently today. `release-migration.md` says this is the place to fix that. | [session-carve.md](session-carve.md) 1a-iv |
| **`$HOME` credential mount becomes opt-in** | A change to who authenticates by default. Design settled (D95); the build is phased behind the proxy, but the default flip may be separable. | [DF39](../findings-unresolved.md) |

## Out — deferred deliberately

- **DF131** (rule-2 sweep gate) — highest-value unbuilt gate, and neither breaking nor schema. Land it
  whenever; it does not need a release.
- **DF127** (tart's legacy VM matcher) — a deprecation with a 2027-01-17 review date. Retiring it *is*
  the plan.
- **DF129 / DF130** (provenance-hook mention-vs-read; the register's absence blindness) — tooling.
- **DF132** (rules in the noticing register) — principle edits, owner's call, no release coupling.
- The public-layering carve (DF31/32/33/34/41/42) — breaking for embedders eventually, but blocked on
  Shape and far too large to ride opportunistically. It needs its own release; that is the right
  answer, not this one.

## Owed at the tag — mechanical

- [ ] **`LibrarySchemaReleases` gains `{Schema: 5, Tag: "v0.9.0"}`** (`internal/config/schema_releases.go`).
      Deliberately omitted until now: that table asserts "this schema shipped in this tag", so only a
      real released tag may be added. Nothing else records schema 5's release, and `PriorReleaseRange`
      is what tells a blocked user which build can still read their data dir.
- [ ] **`## Unreleased` drains** into a `## v0.9.0` heading, marker left in place (D117). `release.yml`
      already enforces that it is empty at the tag — that check is the *drain* half; the *filing* half
      is now `scripts/check_breaking_changes.py` on the PR.
- [ ] **Deprecation register reviewed** — nothing is due (oldest: 2026-09-12), so this is a glance, not
      work. [deprecations.md](../../deprecations.md).
- [ ] **`releasetest` on Linux and macOS** — three backends are platform-locked. Already green for D126
      on both as of 2026-07-17; re-run if scope lands.

## Verified stale — drain, do not "finish"

Checked 2026-07-17 by reading, because each *looked* like an item lost from a previous release:

- **`release-migration.md`'s unticked box** for the public-API reshape + the `agent_files` inner
  json-tag change → `docs/BREAKING-CHANGES.md` already carries `agent_files` entries. The work landed;
  the checkbox never got ticked. Drain the doc, do not redo the work.
- **`copy-mode-history.md`'s "confirm the exact spellings"** (`copy-strict` / `--copy-strict` /
  `copy_strict`) → **shipped in v0.7.0** and present in v0.8.0 and v0.9.0-rc.1. The names are frozen
  user surface; renaming now would be a break, not a free choice. An earlier survey read this open
  question as "naming is free now, land it this release" — it is exactly backwards, and that is what
  a stale open question does to whoever reads it next.
- **`post-merge-roadmap.md`'s** *"None of this needs a second on-disk migration (D103 made `system
  migrate` the last one)"* → false twice over: schema 4 (v0.6.0) and schema 5 (this release) both
  landed since. Its own DF103 says the file's statuses lag.

**The pattern worth noticing:** all three are docs asserting a claim about *other work's state*,
which nothing keeps in step. That is D121 (don't denormalize) and DF103, and it is the argument for
why this file is an index of pointers rather than a summary — and why it gets archived rather than
maintained.
