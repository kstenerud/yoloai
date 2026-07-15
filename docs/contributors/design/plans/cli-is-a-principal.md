> **ABOUTME:** Proposes giving the CLI the principal it already has on disk and deleting the
> empty-principal sentinel that let a hardcoded prefix ship. Scoped, not yet started; breaking.

# Plan: the CLI is a principal — name it, and delete the empty-principal sentinel

**Status:** Scoping, 2026-07-15. Proposed after DF98's third instance landed. **Not started; needs
maintainer decisions (see [Open questions](#open-questions)) before any code moves.** Breaking change
under [AGENTS.md rule 1](../../../../AGENTS.md); name invalidation under rule 2; wants a D-entry.

## The one-sentence version

The CLI already *is* a principal called `cli` on the DataDir axis, and forgets to say so on the
runtime-namespace axis. `""` is the sentinel that covers for the omission, and it is what made a
hardcoded `"yoloai-"` correct in the common case — so three of three eligible backends shipped it.

## Why now

DF98 is not a typo repeated three times. It is one defect with a 100% hit rate on the population
that could have it:

| Backend | Derives a host path from an instance name? | Had the bug |
| --- | --- | --- |
| tart | yes | yes |
| seatbelt | yes | yes |
| containerd | yes | yes |
| docker, podman, apple | no — state lives in a daemon | structurally immune |

The three that were immune were not more careful; they never write the mapping. **Every backend that
could make this mistake did.** With a pluggable `runtime.Backend` as innovation token #1
(`general-principles.md` §2), backend #4 is expected, and it will write the same mapping.

All three are fixed and unit-pinned. This plan is about the class, not the instances.

## The defect

`internal/config/names.go`:

```go
func InstancePrefix(p PrincipalSegment) string {
	if p == "" {
		return "yoloai-"          // ← the whole problem
	}
	return "yoloai-" + string(p) + "-"
}
```

Only integrators pass `ClientCreateOptions.Principal`. Every CLI user gets `""`. So
`InstancePrefix("")` is **exactly** the string a developer would hardcode, which means:

- hardcoding `"yoloai-"` is **correct on the path everyone runs**, including every smoke test;
- it is wrong **only** for integrators who set a principal — the population least able to report it;
- so the bug is invisible at authoring time, invisible in review, and invisible in CI.

That is not a discipline failure. It is a design that makes the wrong code work.

### Why `""` exists

[D62](../../decisions/working-notes.md#d62--principal-namespacing-deterministic-yoloai-principal-name-p8n56-no-library-hashing)
introduced `yoloai-<principal>-<name>` to fix a principal-*blind* `store.InstanceName`, and
deliberately kept the old shape working — *"`MaxNameLength` stays 56 — **no breaking change**"*.
`""` → `"yoloai-"` preserves pre-D62 names. It is a compatibility shim that has outlived its reason.

### Why the CLI has no principal

Same reason, and it contradicts D62's own rule:

> *"The **daemon supplies** a bounded, charset-safe, unique `PrincipalSegment`… The library only
> **validates** the segment; it never derives, hashes, or truncates one."*

Embedders name themselves. The CLI is an embedder that abstains. **And it already has the name**:
`cliutil/layout.go`'s `LayoutForDataDir` roots the library at `TOP/library` *"so the CLI's own state
(`TOP/cli`) can sit beside it without clashing"* — the CLI is a principal called `cli` on the
filesystem axis ([D59](../../decisions/working-notes.md): "the deployment/principal seam runs
*through* `DataDir`"). It simply never carries that identity into the one axis D62 exists to fix.

## The change

1. **The CLI supplies `"cli"`.** One construction site (`cliutil.Layout()` / `SetRootLayoutFromFlag`).
   Instances become `yoloai-cli-mybox`. Budget: `7 + 3 + 1 + 56 = 67 ≤ 76`, inside D62's ceiling with
   room (`MaxPrincipalLength` is 8; an 8-char principal is 72).
2. **`""` becomes invalid.** `ParsePrincipalSegment("")` returns a `*UsageError` instead of the
   default sentinel.
3. **`InstancePrefix` loses its branch** — `return "yoloai-" + string(p) + "-"`, unconditionally.
   There is then no bare `"yoloai-"` to hardcode, because the function cannot produce one.
4. **Migrate existing sandboxes** (see below).

The payoff is the point: after (1), a hardcoded `"yoloai-"` strip breaks **immediately, for every
user, on the path every smoke test exercises**. The class becomes unshippable rather than merely
fixed. After (3) it becomes unwritable.

## Migration

This is the expensive part, and the reason this is a plan rather than a patch.

Existing sandboxes' backend instances are named `yoloai-X`. After (1) the CLI resolves
`yoloai-cli-X`, finds nothing, and **orphans every existing sandbox** — the container/VM keeps
running, unreferenced, holding a quota slot and disk. Silent orphaning of a running agent's
container is the worst possible failure here.

Precedent exists: `internal/orchestrator/migrate_overlay.go` (v3→v4) runs behind the
`store.Record` `SchemaVersion` bump, and `internal/migrate/` has the driver/lock/plan machinery.

Per-backend rename support must be established before committing to rename-over-recreate:

| Backend | Rename mechanism | Verified? |
| --- | --- | --- |
| docker | `docker rename` | no |
| podman | inherits docker path | no |
| tart | `tart rename` (exists in the CLI's verb list) | no |
| containerd | container id is immutable; likely recreate | no |
| seatbelt | no daemon — a directory + pidfile rename | no |
| apple | unknown | no |

**Nothing in that table is verified.** It is the first work item, not a claim (D119).

## Phases

- **P0 — decide.** The [open questions](#open-questions) below. No code until they are answered.
- **P1 — establish the rename table.** Verify per-backend rename support on real backends. This
  decides P3's shape and may force recreate-only for some, which changes the UX.
- **P2 — CLI adopts `"cli"`.** Single site. Every backend's tests already run with a non-empty
  principal (`testutil.UniqueTestPrincipal`), so the suites should stay green; if one breaks, it has
  the DF98 bug and that is the gate working.
- **P3 — migration** behind a schema bump: rename or recreate per P1's findings, with the running-
  instance case handled explicitly.
- **P4 — reject `""`.** `ParsePrincipalSegment` errors; `InstancePrefix` loses its branch. Separable
  from P2 and breaking for integrators independently, so it can land on its own schedule.
- **P5 — sweep the surfaces** (rule 2): `internal/cli/helpcmd/help/*.md` (embedded, shipped, nothing
  typechecks it), README, docs examples, and anything showing `yoloai-<name>` in `docker ps` output.
  `make check` catches none of this.
- **P6 — `BREAKING-CHANGES.md`** under `## Unreleased` (rule 1; D117 made the marker permanent).
- **P7 — verify.** `releasetest` on macOS **and** Linux: the migration touches every backend, and
  three of them are platform-locked (containerd Linux-only; tart/seatbelt/apple macOS-only).

## Open questions

1. **What do integrators who omit `Principal` get?** D62's rule says the library never derives one,
   which argues for a hard `*UsageError` — every embedder names itself. But that breaks every
   existing integrator who omits it, and yoloAI is a public-beta library. The alternative (a `lib`
   default) reintroduces a sentinel, just a louder one. *Erroring is faithful to D62; defaulting is
   kind. Pick one deliberately.*
2. **Rename or recreate?** Rename preserves a running agent's container and its work. Recreate is
   simpler and uniform across backends but destroys in-flight state — unacceptable for a running
   agent, arguably fine for a stopped sandbox. A hybrid (rename where supported, refuse-and-instruct
   otherwise) is more code and more surface.
3. **Running instances during migration.** Refuse to migrate while anything runs, stop-migrate-start,
   or rename live? Backends differ.
4. **Does this plan scope profile image tags too, or leave them to B2?** `profile_build.go:74` and
   `profile.go:274` tag as `"yoloai-" + name`, with no principal component, so two principals with a
   profile named `web` collide on `yoloai-web` in a shared daemon and the second build wins. This is
   **already known**: `research/principal-isolation.md` §B2 (MAJOR, commissioned by D59) frames it and
   recommends the answer — *"any principal-authored build artifact (custom profile/image) lives in the
   principal partition with a principal-scoped image tag"*. Keep the two halves apart when deciding:
   **principals are a namespacing mechanism, not a security boundary.** The embedder chooses which
   principals to install and they self-organize, the way Objective-C prefixes do; D62 says as much —
   it resolved B1 (namespacing) and left *"isolation-grade enforcement … the embedder's job per
   D58/D59"*. So B2's code-execution framing is the embedder's problem and out of this plan's scope.
   What is in scope, if anything, is the plain namespacing gap: D62 scoped instance names and left
   image tags unscoped, which is the same axis one artifact over. Cheap to include here (it is the
   same `InstancePrefix` call); equally defensible to leave to B2's own plan, which owns the wider
   question of whether principals may author profiles at all.
5. **Is `cli` the right name?** It is what `TOP/cli` already uses, which is the argument for it.
   `MaxPrincipalLength` is 8, so there is room for something longer if a better name exists.

## Risks

- **Orphaning.** A half-applied migration leaves containers nothing references. Mitigate: the
  migration is the only thing that may rename, it is idempotent, and `system prune` learns the old
  prefix for one release.
- **Third-party scripts** matching `yoloai-<name>` in `docker ps` break. That is the rule-1 entry's
  job to state.
- **The `""` rejection (P4) is independently breaking** for integrators. Landing P2 without P4 leaves
  the sentinel — and therefore the latent bug — alive for anyone who omits `Principal`. Landing both
  doubles the blast radius of one release. Sequencing is a judgment call.

## What this does not fix

Nothing here makes the *lexical* problem gateable. `"yoloai-"` still names several things (base
image, guest paths, netns marker, profile tags) and a linter still cannot tell which is which — see
DF98 for why a lexical ban on the prefix was rejected. This plan removes the class by making the wrong code fail loudly
on the common path, which is the poka-yoke answer rather than the lint answer.

## References

D58, D59, [D62](../../decisions/working-notes.md#d62--principal-namespacing-deterministic-yoloai-principal-name-p8n56-no-library-hashing)
(principal namespacing), D117 (BREAKING-CHANGES marker), D119 (verify before asserting),
D121 (don't denormalize); [DF98](../findings-unresolved.md) (the three instances);
`internal/config/names.go`, `internal/cli/cliutil/layout.go`, `internal/orchestrator/launch/launch.go:146`,
`client.go:124`.
