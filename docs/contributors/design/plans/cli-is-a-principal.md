> **ABOUTME:** Proposes giving the CLI the principal it already has on disk and deleting the
> empty-principal sentinel that let a hardcoded prefix ship. Breaking change (AGENTS.md rule 1).

# Plan: the CLI is a principal — name it, and delete the empty-principal sentinel

- **Status:** PLANNED — **P0 answered and P1 run (2026-07-16); P2 onward not started.** Scoped
  2026-07-15 after DF98's third instance landed. Breaking change under
  [AGENTS.md rule 1](../../../../AGENTS.md); name invalidation under rule 2; still wants a D-entry
  superseding D62's CLI-elision bullets (owed — see [Open questions](#open-questions)).
- **Depends on:** —

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

### A fourth instance the class already has: prune sweeps are not principal-disjoint (DF115)

DF98's three were *hardcoded* prefixes. This one needs no hardcoding — it derives the prefix
correctly and is still wrong, because `InstancePrefix("")` = `"yoloai-"` is a **prefix of every other
principal's namespace**:

```
InstancePrefix("")      == "yoloai-"          // the CLI
InstancePrefix("acme")  == "yoloai-acme-"     // an integrator
strings.HasPrefix("yoloai-acme-foo", "yoloai-")   // true
```

Tart's and seatbelt's prune select orphans with `strings.HasPrefix(name, InstancePrefix(principal))`
(`runtime/tart/prune.go:31,36`, `runtime/seatbelt/prune.go:80`). `known` is built from the caller's
*own* `SandboxesDir` (`system.go` `classifySandboxes`), so another principal's sandboxes are never in
it. Net effect: **`yoloai system prune` from the CLI reaps every integrator's instances** — the exact
cross-principal destruction principal-scoping exists to prevent (DF19).

Docker is immune, and for the reason that indicts the other two: it filters by label **equality** via
`runtime.IsOrphanCandidate` — `labels[LabelPrincipal] == string(principal)` (`runtime/orphan.go:30`)
— which is what [D62](../../decisions/working-notes.md) actually specified: *"runtime enumeration
filters by label, not by name-string splitting."* Tart and seatbelt never implemented that half, and
prefix-matching only *looks* equivalent while one principal is empty.

D62:380's non-collision proof covers **names** (`yoloai-foo` vs `yoloai-acme-foo` never collide) and
says nothing about **prefix containment**. So the proof is sound and the guarantee it implies is not.
Two independent fixes fall out, and this plan only needs the first: naming the CLI `cli` makes every
namespace mutually disjoint and the whole class evaporates structurally; separately, tart and
seatbelt should adopt `IsOrphanCandidate` per D62:379 regardless. Filed as **DF115**.

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

Per-backend rename support must be established before committing to rename-over-recreate.
**P1 was run on Apple Silicon 2026-07-16** (docker daemon up, podman + apple `container` + tart
installed); four rows are settled, one is partial, one needs a Linux host:

| Backend | Rename mechanism | Verified? |
| --- | --- | --- |
| docker | `docker rename` | **yes** — exit 0 on a **running** container (stays running) and on a stopped one |
| tart | `tart rename` | **yes** — exit 0 on a **running** VM (stays listed running) and on a stopped one |
| seatbelt | **none needed** — nothing on disk carries the prefix | **yes**, by reading (see below) |
| apple | **no rename verb exists** | **yes** — the full subcommand list (copy/create/delete/exec/export/inspect/kill/list/logs/run/start/stats/stop/prune) has no `rename`/`mv` |
| podman | `podman rename` | **partial** — the verb exists (podman 5.8.2); not functionally exercised (no machine running here) |
| containerd | container id is immutable → recreate | **no** — Linux-only, not verifiable on this host. D62's own verification stands as the argument: the name is *both* container id and snapshot key, daemon-enforced (`validate.go:34-42`, `containerd/v2@v2.2.2`) |

**Seatbelt needs no migration.** Its instance name is derived, never stored: the on-disk dir is the
*bare* sandbox name, and `sandboxName()` strips `config.InstancePrefix(r.layout.Principal)` off the
computed instance name (`runtime/seatbelt/seatbelt.go:160`, `runtime/seatbelt/prune.go:80-84`). So
`InstanceName("cli","foo")` → `yoloai-cli-foo` → dir `foo` — identical to today's `yoloai-foo` → dir
`foo`. Running sandboxes are unaffected too: the process argv encodes the dir name, not the instance
name. The plan's earlier guess ("a directory + pidfile rename") was wrong.

**This forces the hybrid (open question 2), rather than leaving it a preference.** Live rename works
on the two backends that most need it, and seatbelt is free — but apple has *no rename verb* and
containerd's name *is* its id, so both need recreate-or-refuse whatever the preferred UX. A uniform
rename is not available; a uniform recreate would destroy in-flight state on backends that can
rename perfectly well.

**Caveat on the two live renames.** Both were verified as "the CLI accepts it on a running instance
and the instance is still listed running afterwards". Whether yoloAI's own handles survive — the live
`tart run` host process re-binding, docker's stored labels, an attached tmux session — was **not**
established, and P3 must not assume it.

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

**Answered by the owner, 2026-07-16** — Q1 and Q5 are settled; Q2 is settled by P1's findings rather
than by preference. Q3 and Q4 remain open. The framing behind the answers, in the owner's words:
*every caller supplies a principal, and a default principal is a violation of the prohibition against
default values.* Collisions between principals are **not** a security matter but a naming one — sorted
out among the principals themselves and chosen at installation time by the user, the way Objective-C
prefixes are. (This is the same framing Q4 below already reached independently.)

The owner's account of how `""` survived is worth recording, because it changes the entry the D-entry
must write: the elimination of these defaults was the intent behind commissioning the migration
framework, and this change was meant to be *in that batch* — one major breaking change, gotten over
with. D60/D61 (the framework) are dated **2026-06-02**; D62 (which chose "no breaking change" and
built the sentinel to avoid a migration) is dated **2026-06-03**. The machinery was one day old and
built for exactly this class of change. This was an omission, not a trade-off anyone weighed.

1. **What do integrators who omit `Principal` get?** — **ANSWERED: a hard error.** No default, no
   `lib` sentinel. "Every caller should provide a principal; a default principal is a violation of the
   prohibition against default values." Faithful to D62's own rule (the library never derives one) and
   to D58's invariant. The public-beta cost is accepted: the next release is already breaking, and a
   quiet default is the defect being removed, not a kindness worth keeping.
2. **Rename or recreate?** — **SETTLED BY P1, not by preference: the hybrid is forced.** Live rename
   works on tart and docker (both verified exit 0 on running instances), and seatbelt needs no
   migration at all. But apple has **no rename verb in its CLI** and containerd's name *is* its
   container id and snapshot key — neither can rename at any price. So "rename where supported,
   refuse-and-instruct otherwise" is the only shape available: uniform-rename is impossible, and
   uniform-recreate would destroy in-flight state on the backends that can rename perfectly well.
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
5. **Is `cli` the right name?** — **ANSWERED: yes, `cli`.** The owner's reason is the one this plan
   already gives: it matches the existing identifiers and machinery of the CLI (`TOP/cli`). Budget is
   fine at 3 chars (`7 + 3 + 1 + 56 = 67 ≤ 76`).

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
