> **ABOUTME:** The standing staging list for whatever ships next — permanent, drained and reset at
> every release. It points at the records that own each item and never carries their state; "what's
> left?" is answered by following the links, not by reading this file.

# Next release

**Next release version: `v0.9.0`** — escalated from `v0.8.1` by the two breaks now sitting in
[`BREAKING-CHANGES.md`](../BREAKING-CHANGES.md) `## Unreleased`: the config-command path validation
(first, and what tripped it), and D126's principal-scoped instance names.

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

- [DF113](design/findings-unresolved.md) — `destroy` frees the sandbox name while leaving the
  instance behind, so the next `start` adopts a VM it never provisioned
- [DF53](design/findings-unresolved.md) — Tart silently ignores `-p` port mappings (port-forwarding
  never wired into `tart run`)
- [init-sentinel](design/plans/init-sentinel.md) — mark an in-progress TOP init, so a failed one is
  a fact and not an inference

## Candidates — undecided

*None.* Decided out 2026-07-17 — each record's `**Rides:**` field says why:
[module-split](design/plans/module-split.md), [session-carve](design/plans/session-carve.md) 1a-iv,
[DF39](design/findings-unresolved.md), [DF104](design/findings-unresolved.md).

## In flight — started, not finished

*Nothing.*

The items are not what gets lost — **the stack is**. Fixing A reveals B, C and D; D is urgent so we
jump; A is never resumed, and nothing records that it was interrupted or by what. **When you jump,
write the line here first**: what was abandoned, how far it got, what took priority. The jump is an
event you can see; the intention to come back is not.

## Taking stock — verified stale, do not redo (2026-07-17)

Each looked like an item lost from an earlier release. Each had already landed; only the doc was
stale (DF103's class — a claim about other work's state that nothing keeps in step).

- `design/plans/release-migration.md`'s unticked `agent_files` box → the entries are in
  BREAKING-CHANGES.
- `design/plans/copy-mode-history.md`'s *"confirm the exact spellings"* → `copy-strict` shipped in
  **v0.7.0**. Frozen user surface; renaming now would be a break, not a free choice.
- `design/plans/post-merge-roadmap.md`'s *"no second migration needed"* → false twice; schema 4 and
  5 both landed.

## The release ritual

1. **`releasetest` on Linux and macOS** — three backends are platform-locked, so neither host alone
   is evidence.
2. On the owner's go-ahead:
   - Drain `## Unreleased` into a `## vX.Y.Z` heading; leave the marker (D117). `release.yml` fails
     the tag if it is non-empty.
   - If a schema shipped, add `{Schema: N, Tag: "vX.Y.Z"}` to `LibrarySchemaReleases`
     (`internal/config/schema_releases.go`) — that table asserts *shipped* tags, so the entry cannot
     be written before the tag exists. **Owed now: `{Schema: 5, Tag: "v0.9.0"}`.**
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
