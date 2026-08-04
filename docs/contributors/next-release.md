> **ABOUTME:** The standing staging list for whatever ships next — permanent, drained and reset at
> every release. It points at the records that own each item and never carries their state; "what's
> left?" is answered by following the links, not by reading this file.

# Next release

**Next release version: `v0.11.0`** — escalated from `v0.10.1`: the sandbox image moved from
Node.js 20 to Node.js 22 LTS, and the sandbox directory became three access tiers (`host/`, `ro/`,
`rw/`), changing the on-disk layout and requiring a migration. Both are user-visible (see
`BREAKING-CHANGES.md`, Unreleased).

## How this works

**Permanent, like `## Unreleased`.** This file is never renamed and never archived. Releasing
**drains** it back to the initial state below — the same shape D117 fixed for BREAKING-CHANGES, and
for the same reason: a marker that is always present is always where you need it.

**The version field is a fact, not a plan.** It starts at the next point release (`0.y.z+1`; once
out of beta, `x.y.z+1`) and **escalates the moment something breaking lands on main** — during beta
to `0.y+1.0`. Escalation is a consequence of what landed, not a decision someone makes. Its use is
the inverse: once the field says a breaking release, *slipping in more breakage costs users nothing
they are not already paying*, which is the only argument for pulling work forward. The test for
anything below is therefore **"would this wait for the next point release?"** If yes, it waits.

**This file points; it does not track.** Every item's own finding, decision or plan owns its status
and progress. A checkbox here would be a second copy of that state and would drift (D121), so there
are none. **"What's left for this release?"** is answered by following the links and reading each
record — never by trusting this page.

**So an entry stays until the release drains it — finishing the work does not remove the line.**
"Done" is the record's to say, not this list's. Removing an entry on completion looks like tidying
and is the same mistake as a checkbox: it makes the page's *length* the status, so the page starts
answering "what's left?" by itself, and the links stop being the answer. It also destroys the only
record of what the release contained, at the exact moment that record becomes useful. If the record
moved when it resolved (findings-unresolved.md → findings-resolved.md), repoint the link and leave
the entry.

**A line here is an ID and the record's own title. Nothing else — no reason, no scope note, no
"just the small half".** Every entry below links to a record that owns *why* it is here, in a
`- **Rides:**` field naming the kind of release its fix needs, and which half of it if only one
qualifies. That field is the thing to read; this page is an index to it.

**Why the rule is this blunt (2026-07-17).** Six candidates were assessed and *two* were decided
wrongly — in opposite directions. One line asserted something its record contradicted, and was
believed. Another asserted something true its record did not contain, and was overturned as
unsupported. Both were reasons composed *here*, and that is the single property they share: a
page-authored reason cannot be checked against anything, so being right is indistinguishable from
being wrong. The two candidates settled quickly and correctly were the two whose records already
held the deciding fact. Length was never the variable — **ownership was**. So the reason lives in
the record, always, and this page is not permitted to explain itself.

**Getting here.** When a release is on the table, take stock of what landed since the last tag and
decide what is best slipped in before the cut. Anything discovered along the way gets its own
finding or decision first, and lands here only if it should block the release.

## In scope

*Entries stay until the release drains this file. Do not remove one because it is finished — see
"This file points" above.*

- [DF149](design/findings-resolved.md) — `reset` stranded `files/`, `cache/` and `agent-runtime/` at
  deleted inodes, silently breaking the agent's artifact channel
- [DF156](design/findings-resolved.md) — a `yoloai-base` rebuild did not invalidate profile images
  built on the old base
- [DF157](design/findings-resolved.md) — on the restart path every line of launch progress was
  rendered as a warning
- [DF158](design/findings-resolved.md) — the base image pinned Node 20 on an unverified gVisor
  claim, capping Claude Code and shipping an EOL runtime **(the breaking change: Node 20 → 22)**
- [DF160](design/findings-unresolved.md) — `dind/podman-priv` times out on macOS, and the smoke
  harness turns a slow teardown into a crash (harness half fixed; the timeout is not)
- [DF159](design/findings-unresolved.md) — `exec start: ttrpc: closed` on containerd-vmenhanced
  during `apply`, once in 38 runs (filed, not fixed — listed because its docs correction ships)
- [sandbox-share-tiering.md](archive/plans/sandbox-share-tiering.md) — sandbox directory share
  tiering (host-only / read-only / read-write), closing DF136 and DF148
- [DF161](design/findings-resolved.md) — mount conformance was skipped on the only two backends
  whose mounts are unusual, over one hardcoded path
- [DF168](design/findings-resolved.md) — `system migrate` plans the framework migrators before the
  sealed ladder runs, so an install with pre-v3 records cannot be upgraded by any release since
  v0.6.0
- [DF162](design/findings-resolved.md) — seatbelt's `:ro` mounts were not read-only whenever a
  broader rule granted write **(the second breaking change: `:ro` is now enforced)**
- [DF175](design/findings-unresolved.md) — `yoloai files put --overwrite` silently delivers
  fabricated content to a running tart sandbox
- [DF181](design/findings-unresolved.md) — the DF175 repair gives up inside one revalidation tick,
  so `files put` fails on a guest that is about to be correct

## Candidates — undecided

- [upgrade-coverage.md](design/plans/upgrade-coverage.md) — automated coverage for upgrading an
  existing install
- [host-controlled-agent-launch.md](design/plans/host-controlled-agent-launch.md) — yoloAI decides when the
  agent starts (the enabler for an untamperable firewall)
- [tamper-resistant-network-isolation.md](design/plans/tamper-resistant-network-isolation.md) —
  the network allowlist enforced outside the agent's reach, on every backend or with evidence why
  not
- [DF171](design/findings-unresolved.md) — gVisor is excluded from agent-free launch by a
  username-resolution bug the codebase already fixed elsewhere

## In flight — started, not finished

*Nothing.* `sandbox-share-tiering` **merged into this branch 2026-08-02** (`026c7650`) — complete,
green on both platforms, and deliberately still off `main`, because the migration it carries lands
last in the release and stays revisable until then.

**Unmerged branches, surveyed 2026-08-02 — all six resolved.** Five are absorbed or archaeology and
can be deleted; one (`testing/backend-conformance`) held a finding nobody had filed, now recovered.
Verified by *content* on the release branch, not by whether the commit was merged, because four of
the six landed their work through a different path than the branch that started it:

| Branch | Ahead | What it is |
| --- | --- | --- |
| `sandbox-share-tiering` | — | **Merged into this branch.** |
| `microvm-backend` | 16 | The QEMU-microvm spike, **superseded by D104** (retired: custom-kernel-only, no isolation gain over Kata). Archaeology, and the only branch not verified line-by-line — D104 is the decision that covers it. Delete or leave; not pending work. |
| `base-trixie` | 2 | **Absorbed — verified 2026-08-02.** Both halves are on `main`: the Dockerfile carries `debian:trixie-slim` and `binutils-gold`, and both idiosyncrasies sections are present. Safe to delete. |
| `broker-podman-rootless` | 2 | **Absorbed — verified 2026-08-02, and it was the one I expected to hold real work.** The research shipped *fuller* than the branch (main names the implemented `runtime/podman/reach.go`; the branch predates it), DF56 is filed and resolved, and the `wip(broker)` `AgentFreeLaunch` flip is explicitly declared moot by [egress-proxy-build.md](design/plans/egress-proxy-build.md) — "podman just needs its slirp `InjectorReach`", which is what shipped. Safe to delete. |
| `sandbox-hostname` | 2 | **Absorbed — verified 2026-08-02.** `runtime.InstanceConfig.Hostname` and its backends are on `main`, and DF142 is filed *and resolved*. Safe to delete. |
| `testing/backend-conformance` | 1 | **Content recovered 2026-08-02**, not cherry-picked: DF28 was unfiled anywhere, and its pointers named the pre-D99 `internal/runtime/…` paths. Re-filed with paths corrected and its relationship to DF159/DF160 named. Branch safe to delete. |

**And one piece of debris:** a `release-prep` branch still exists from the v0.6.0 era — 0 ahead of
`main`, 408 behind. It is why this branch is named for its version instead.

The items are not what gets lost — **the stack is**. Fixing A reveals B, C and D; D is urgent so we
jump; A is never resumed, and nothing records that it was interrupted or by what. **When you jump,
write the line here first**: what was abandoned, how far it got, what took priority. The jump is an
event you can see; the intention to come back is not.

## Taking stock — verified stale, do not redo

*Nothing yet.* When a release is on the table, this holds what looked like lost work but had already
landed — verified-stale doc claims not to "finish". Scoped to one release cycle; emptied at each cut.

## The release ritual

1. **`releasetest` on Linux and macOS** — three backends are platform-locked, so neither host alone
   is evidence.
2. On the owner's go-ahead:
   - Drain `## Unreleased` into a `## vX.Y.Z` heading; leave the marker (D117). `release.yml` fails
     the tag if it is non-empty.
   - If a schema shipped, add `{Schema: N, Tag: "vX.Y.Z"}` to `LibrarySchemaReleases`
     (`internal/config/schema_releases.go`) — that table asserts *shipped* tags, so the entry cannot
     be written before the tag exists.
   - Glance at [deprecations.md](deprecations.md) — anything past its due date is a retire-or-extend
     call. *(Nothing due until 2026-09-12.)*
   - **Reset this file** to the initial state below, with the version field assuming the point
     release after the one just cut.
3. Commit, push, tag, release.

## Initial state

What "reset" means — restore exactly this, keeping everything above `## In scope` and below
`## The release ritual`:

- **Version field:** the next point release after the one just cut (`0.y.z+1`; post-beta
  `x.y.z+1`), with no escalation note. It escalates again on its own, when something breaking lands.
- **In scope**, **Candidates**, **In flight**: emptied.
- The line format does not reset, because it is not release state: an entry is an ID and the
  record's own title, and the record's `**Rides:**` field carries the reason. See "This file
  points".
- **Taking stock**: emptied — its notes are scoped to one release cycle.
