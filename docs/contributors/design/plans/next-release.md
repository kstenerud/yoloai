> **ABOUTME:** What goes into v0.9.0, what waits, and what was abandoned mid-flight. A checklist,
> not an analysis — every line points at the record that owns the detail.

# v0.9.0 — what goes in

- **Status:** IN-PROGRESS — assembled 2026-07-17. Archives when v0.9.0 ships (rule 8).
- **Depends on:** —

**Why 0.9.0 takes extra breakage:** it already breaks (D126: instance names, the empty principal)
and already ships a migration (schema v4→v5). Slipping more breakage in costs users nothing they
are not already paying. **For 0.9.1 the answer is no** — that is the whole test for this list.

## Going in

- [ ] **[DF126](../findings-unresolved.md)** — profile image tags carry no principal. Breaking (a
      user-visible tag renames) and schema-touching (`ImageRef` is persisted). Latent, so it will
      never justify its own break: it rides this release or it does not happen.
- [ ] **[init-sentinel](init-sentinel.md) P1** — resolves DF128. Small; drop it if the release is
      tight. P2 (retry) is not proposed.

## Your call

- [ ] **[DF104](../findings-unresolved.md)** — `--network-isolated` is IPv4-only and says so nowhere.
- [ ] **[DF53](../findings-unresolved.md)** — tart silently drops `-p`. Minimum: make it *reject*.
- [ ] **[DF113](../findings-unresolved.md)** — `destroy` frees the name, next `start` adopts the
      leftover. Wants a provenance field in metadata = schema.
- [ ] **[module-split](module-split.md)** — substrate store shape; self-described as "another
      versioned migration".
- [ ] **[session-carve](session-carve.md) 1a-iv** — `keepalive_only` default flip; reshapes
      `runtime-config.json`.
- [ ] **[DF39](../findings-unresolved.md)** — `$HOME` credential mount becomes opt-in.

## Not this release

DF131 (the rule-2 sweep gate), DF127, DF129, DF130, DF132, the public-layering carve. None is
breaking or schema-touching, so each can land in any release. **Being valuable is not a reason** —
DF131 is the highest-value unbuilt gate in the repo and still does not belong here.

## In flight — started, not finished

*Nothing today.*

This section exists because the items are not what gets lost — **the stack is**. Fixing A reveals B,
C and D; D is genuinely urgent so we jump; A is never resumed, and nothing anywhere records that it
was interrupted or by what. **When you jump, write the line here first**: what was abandoned, how far
it got, and what took priority. One line. The jump itself is the trigger — it is an event you can
see, unlike the intention to come back, which is not.

## At the tag

- [ ] `LibrarySchemaReleases` += `{Schema: 5, Tag: "v0.9.0"}` — deliberately omitted until now; that
      table asserts *shipped* tags only, and nothing else records schema 5's release.
- [ ] Drain `## Unreleased` into `## v0.9.0`, leave the marker (D117).
- [ ] `releasetest` both platforms if any scope lands. Green for D126 as of 2026-07-17.
- [ ] Glance at [deprecations.md](../../deprecations.md) — nothing due until 2026-09-12.

## Don't redo — verified shipped (2026-07-17)

Each looked like an item lost from an earlier release. Each had already landed; only the doc was
stale (DF103's class — a claim about other work's state that nothing keeps in step).

- `release-migration.md`'s unticked `agent_files` box → the entries are in BREAKING-CHANGES.
- `copy-mode-history.md`'s *"confirm the exact spellings"* → `copy-strict` shipped in **v0.7.0**.
  Frozen user surface; renaming now would be a break, not a free choice.
- `post-merge-roadmap.md`'s *"no second migration needed"* → false twice; schema 4 and 5 both landed.
