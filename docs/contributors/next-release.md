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

**Getting here.** When a release is on the table, take stock of what landed since the last tag and
decide what is best slipped in before the cut. Anything discovered along the way gets its own
finding or decision first, and lands here only if it should block the release.

## In scope

- [DF126](design/findings-unresolved.md) — profile image tags carry no principal. Breaking, and
  schema-touching via the persisted `ImageRef`. Latent, so it will never justify its own break: it
  rides a breaking release or it does not happen.
- [init-sentinel](design/plans/init-sentinel.md) P1 — resolves DF128 rather than rewording it.
  Small; droppable if the cut is tight.
- [DF113](design/findings-unresolved.md) — **the provenance stamp only, not the guard.** Writing the
  stamp at `create` is inert and changes no behaviour; the guard that reads it has an unsettled
  design (idempotency) and must not gate the cut. The stamp needs the migration's backfill, so it
  rides now or costs a schema of its own later — and building it later leaves v0.9.0's own sandboxes
  permanently unguessable too.
- [DF53](design/findings-unresolved.md) — **make tart reject `-p`, nothing more.** Newly-rejected
  input is a break (rule 1), so it is cheap only while something else already breaks. Actually
  wiring softnet port-forwarding is a feature needing real macOS verification, and is not
  release-gated — that half waits.

## Candidates — undecided

*None.* Decided 2026-07-17: module-split, session-carve 1a-iv and DF39 are all out — each record
carries the reason.

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
- **Taking stock**: emptied — its notes are scoped to one release cycle.
