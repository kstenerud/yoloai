> **ABOUTME:** The standing staging list for whatever ships next — permanent, drained and reset at
> every release. It points at the records that own each item and never carries their state; "what's
> left?" is answered by following the links, not by reading this file.

# Next release

**Next release version: `v0.12.0`**

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

**A migration escalates the version *and* opens the branch** (D131). The moment work is known to
need one, `release-vX.Y.Z` is cut at the escalated version and that work merges there instead of
`main` — so `main` never carries a migration that has not shipped, and nobody tracking `main` can
be stamped at a schema that never ships in that form. This file is then staged on that branch.

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

*v0.12.0 is the **configuration** release: the config/trust fixes already landed, plus the layering
that makes them structural rather than four spot repairs.*

- [DF195](design/findings-unresolved.md) — `--env` comma-splits its value on `new`/`run` but not on `start`/`restart`/`reset`, so an ordinary value cannot be passed on the path that creates the sandbox
- [D140](decisions/working-notes.md#d140--yoloaiyaml-project-config-is-removed-entirely) — `.yoloai.yaml` project config is removed entirely
- [D142](decisions/working-notes.md#d142--the-mounts-configprofile-key-is-retired-directories-is-its-strict-superset) — The `mounts:` config/profile key is retired; `directories:` is its strict superset
- [DF209](design/findings-unresolved.md) — `isolation` and `model` still carry from personal defaults into a profile, by a second route DF207/DF208 did not close
- [D143](decisions/working-notes.md#d143--configuration-is-resolved-from-provenance-tagged-layers-not-merged-eagerly-at-each-boundary) — Configuration is resolved from provenance-tagged layers, not merged eagerly at each boundary
- [config-provenance-layers.md](design/plans/config-provenance-layers.md) — Config provenance layers — one resolver, one policy table
- [DF210](design/findings-unresolved.md) — a profile's `os:` is parsed, merged, and never read
- [DF211](design/findings-unresolved.md) — `--no-profile` cannot change any outcome, and its help text describes a feature that does not exist
- [DF201](design/findings-unresolved.md) — `agent_files` list form copies credentials the string form strips
- [DF202](design/findings-unresolved.md) — a file-defined agent can make yoloAI read arbitrary vars from its own environment
- [DF206](design/findings-unresolved.md) — config and profile `network.allow` entries are silently discarded whenever the mode is set explicitly
- [DF207](design/findings-resolved.md) — personal defaults leak into profiles, contradicting a bold documented guarantee (RESOLVED 2026-08-13)
- [DF208](design/findings-resolved.md) — the DF207 leak's sibling: restart/relaunch resolved `agent_args`, `agent_files`, and `env` for a profile-attached sandbox from personal defaults, not baked-in defaults (RESOLVED 2026-08-13)

## Deferred to v0.13.0 — the network release

*Not "dropped" and not "done": scoped out of this cut, deliberately, on 2026-08-14. Both remaining
plans build on [D143](decisions/working-notes.md#d143--configuration-is-resolved-from-provenance-tagged-layers-not-merged-eagerly-at-each-boundary)'s
provenance — D141 must tell operator-authored from repo-derived, and the network mode validation must
tell a typed port from an inherited one — so building either first means building provenance twice,
in two places, and then owning both. **The migration rides with `network-mode-reshape.md`, so v0.12.0
carries none** and rule 12's constraint applies to v0.13.0's branch rather than this one.*

- [enforcement-build.md](design/plans/enforcement-build.md) — Host-side enforcement — build brief
- [DF188](design/findings-unresolved.md) — `resolve_domains` accepts whatever a resolver returns, so a sinkholed allowlist domain installs a rule that matches nothing and says nothing
- [DF189](design/findings-unresolved.md) — yoloAI's CNI subnet is byte-identical to podman's default allocator pool, so two sandboxes on one host can hold the same address
- [DF190](design/findings-unresolved.md) — an apple sandbox silently loses all egress when an unrelated sandbox restarts and reclaims its vmnet bridge index
- [DF193](design/findings-unresolved.md) — a guest can pre-create the on-create-done marker, and the host promotes it to permanent state on the next start, so setup commands never run
- [DF194](design/findings-unresolved.md) — a guest holding `CAP_NET_ADMIN` can unbind its own host-side enforcement by destroying its own interface, and the sandbox returns unenforced unless something reinstalls
- [network-mode-reshape.md](design/plans/network-mode-reshape.md) — Network mode reshape — one flag, four trust boundaries
- [DF196](design/findings-unresolved.md) — `--network-none` silently swallows an allowlist: `--network-allow` is accepted, discarded, and never recorded
- [DF197](design/findings-unresolved.md) — `--port` is refused with `--network-none` at the CLI only, so a profile's or archetype's ports are accepted and silently dropped
- [DF198](design/findings-unresolved.md) — `--network-none` is silently unenforced on containerd, apple and tart, while shipped help says it holds on every backend
- [DF199](design/findings-unresolved.md) — `--network-none` makes a seatbelt sandbox fail to start, because SBPL's `network*` class also covers unix sockets
- [DF200](design/findings-unresolved.md) — the `NetworkNone` conformance case cannot fail, and does not run on any backend that has the defect
- [D141](decisions/working-notes.md#d141--a-repo-may-not-widen-the-sandbox-boundary-requests-that-would-are-refused-and-listed) — A repo may not widen the sandbox boundary; requests that would are refused and listed
- [repo-request-trust.md](design/plans/repo-request-trust.md) — Repo-request trust — balk and list, never filter and proceed

## Candidates — undecided

*Nothing yet.*

## In flight — started, not finished

*Nothing.*

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
2. **CI must be green on the exact commit being tagged**, checked on GitHub, not inferred from a
   local `make check`. The two answer different questions: `make check` says "it passes on this
   machine with these tool versions," and only CI says "it passes on the versions the project
   pins." v0.11.0 was tagged on a commit whose CI run then failed — seven `shellcheck` findings
   that a green local gate could not have produced, because the container fallback was pulling a
   newer shellcheck than the runner had ([DF187](design/findings-unresolved.md)). The tool is
   pinned now; the class is not. **Nothing is tagged on an unverified head** — if the head moves
   while you are preparing, re-check the new head.
3. **Check for open Dependabot PRs and bring them to the owner**, each with what it updates and why
   it matters — a security fix, a bug affecting a path yoloAI uses, or routine currency. The owner
   decides whether each rides this tag or waits. They are easy to miss because their branches are
   the one kind the cleanup sweep deliberately leaves alone, so nothing else surfaces them.
4. On the owner's go-ahead:
   - Drain `## Unreleased` into a `## vX.Y.Z` heading; leave the marker (D117). `release.yml` fails
     the tag if it is non-empty.
   - If a schema shipped, add `{Schema: N, Tag: "vX.Y.Z"}` to `LibrarySchemaReleases`
     (`internal/config/schema_releases.go`) — that table asserts *shipped* tags, so the entry cannot
     be written before the tag exists.
   - Glance at [deprecations.md](deprecations.md) — anything past its due date is a retire-or-extend
     call. *(Nothing due until 2026-09-12.)*
   - **Reset this file** to the initial state below, with the version field assuming the point
     release after the one just cut.
5. Commit, push, **wait for CI to go green on that pushed head**, then tag and release. The drain
   commit is a change like any other, so the head you verified in step 2 is not the head you are
   tagging until CI has passed on it too.

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
