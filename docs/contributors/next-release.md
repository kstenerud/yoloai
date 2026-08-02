> **ABOUTME:** The standing staging list for whatever ships next — permanent, drained and reset at
> every release. It points at the records that own each item and never carries their state; "what's
> left?" is answered by following the links, not by reading this file.

# Next release

**Next release version: `v0.11.0`, and that number is in question — see § The v0.10.0 that never
shipped.** Escalated from `v0.10.1` on the assumption that v0.10.0 was cut; it was not. The
escalation itself is sound — the sandbox image moved from Node.js 20 to Node.js 22 LTS, which is
user-visible (see `BREAKING-CHANGES.md`, Unreleased). Only the *starting point* is wrong.

## The v0.10.0 that never shipped

**Found 2026-08-02, opening this file to stage the release.** `docs/BREAKING-CHANGES.md` carries a
frozen `## v0.10.0` heading, and `0d9295ce chore(release): prepare v0.10.0` is on `main` — but **no
`v0.10.0` tag exists**, locally or on the remote. The newest tag is `v0.9.0`. The release was
prepared and never cut, and **82 commits have landed on `main` since**, so the preparation was not
paused, it was passed.

Everything downstream inherited the assumption. This file's version field says "escalated from
`v0.10.1`". The changelog has a frozen section — and the format's own rule is that a `## vX.Y.Z`
heading *means shipped* — describing a release nobody can install. The hostname change it documents
is on `main`, correctly, and has been for 82 commits; it is just filed under a version that does
not exist.

**Two coherent resolutions, and the choice is the owner's:**

- **Cut the next release as `v0.10.0`.** The number is unclaimed, so nothing is lost, and the
  changelog stops naming a phantom. It means merging the frozen `## v0.10.0` section into
  `## Unreleased`, which the preamble forbids for a *shipped* section — the point being that this
  one is not shipped, so the prohibition does not bind. Cheapest, and leaves no artifact claiming
  to be a version.
- **Cut `v0.11.0` and leave the gap.** Nothing to edit and the numbering stays monotonic. The cost
  is permanent: the changelog documents a `v0.10.0` with no tag, no assets and no install path,
  sitting beside `LibrarySchemaReleases`, whose entire job is to name *real* tags a user can
  downgrade to.

**Do not resolve it by tagging `v0.10.0` at the prepare commit.** That would exclude 82 commits of
work from a release cut from a tree that contains them, and the next changelog would have to
explain the discontinuity.

**Either way, one thing is not optional:** the ritual below says to add `{Schema: N, Tag: "vX.Y.Z"}`
to `LibrarySchemaReleases` when a schema ships. Schema 6 ships in this release, and that entry must
name the tag actually cut.

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

## Candidates — undecided

*Nothing yet.*

## In flight — started, not finished

- **`sandbox-share-tiering`** — 47 commits, complete and green on both platforms (Linux
  `make check`; macOS `make releasetest` 19/19). Deliberately **not merged to `main`**: it carries
  the v5→v6 migration, and the owner's rule is that a migration lands last in its release, so it
  stays revisable while the rest of the release is assembled. It should merge into **this branch**,
  which is what this branch is for. Everything it needs is recorded — plan archived, deprecation
  registered, BREAKING-CHANGES entries filed, DF170 addressed in place.

**Unmerged branches, surveyed 2026-08-02.** None of these is tracked anywhere else, which is the
reason for the survey rather than the reason to act on them:

| Branch | Ahead | What it is |
| --- | --- | --- |
| `sandbox-share-tiering` | 47 | Above. Belongs in this release. |
| `microvm-backend` | 16 | The QEMU-microvm spike, **superseded by D104** (retired: custom-kernel-only, no isolation gain over Kata). Archaeology; delete or leave, but it is not pending work. |
| `base-trixie` | 2 | Debian trixie base image. The trixie move already shipped via other commits — these two look like leftovers and need one look before assuming so. |
| `broker-podman-rootless` | 2 | One `wip(broker):` commit plus rootless-podman injector research. Genuinely unfinished. |
| `sandbox-hostname` | 2 | The hostname work is **on `main`** already; these are the DF142 finding plus a duplicate. Probably absorbed. |
| `testing/backend-conformance` | 1 | A single backend-idiosyncrasies note (DF28 Kata readiness race). Cherry-pick or drop. |

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
