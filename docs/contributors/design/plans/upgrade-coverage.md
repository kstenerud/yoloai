> **ABOUTME:** Automated coverage for upgrading an existing install — create a sandbox with a
> released binary, migrate it with the build under test, and then *use* it. The blind spot that
> produced this branch's two worst findings.

# Plan: automated upgrade coverage

- **Status:** PLANNED — designed 2026-08-02, not built.
- **Depends on:** —
- **Rides:** **any.** It is test infrastructure, breaks nothing, and its value is highest *before*
  the next layout change rather than after.

## Why this exists, stated as evidence rather than principle

Nothing in this repo has ever executed an upgrade. `smoke_test.py` creates fresh sandboxes and
`releasetest` creates fresh sandboxes; every test fixture starts from nothing. The migrators have
unit tests, and unit tests are precisely what cannot see this class of defect, because they supply
their own fixtures.

Three findings have now come out of that gap, and they are worth naming because they failed in
different directions — the first two on the sandbox-share-tiering branch, the third during the
pre-release audit that was supposed to be a formality:

- **[DF164](../findings-unresolved.md)** — every pre-v6 migrator addressed the sandbox through the
  *live* path builders, so the tier move silently repointed them at the layout they were migrating
  *to*. They found nothing, reported "nothing to migrate", and let the realm stamp a schema over
  sandboxes never converted. **Their own tests stayed green throughout**, because a commit had
  updated the fixtures to follow the move: fixture and migrator agreed with each other while both
  disagreed with every sandbox on disk.
- **[DF168](../findings-resolved.md)** — `system migrate` refused *every* install with pre-v3
  records, on v0.6.0, v0.9.0 and `main` alike, in both `--check` and apply, with no stepwise path
  out. Shipped, HIGH, and present for two releases.
- **[DF185](../findings-unresolved.md)** — a **closed loop** between two correct refusals: `system
  migrate` said "stop the sandbox", `yoloai stop` said "run system migrate", and only `system
  migrate` is exempt from the out-of-date-directory gate. Plus a downgrade instruction — *recover
  your changes and recreate those sandboxes* — appended to **every** blocked op, including a refusal
  over insufficient disk space. Neither half is a defect in any component; both exist only in the
  sequence an operator walks.

All three were found the same way: a human built a binary from a release tag, created a sandbox with
it, and ran the migration by hand. That is the only detector this project has ever had for the
category, it has been applied three times, and it found something serious every time. The macOS half
of the branch was rehearsed the same way, by hand, because the handoff asked for it.

**And the third one moves the argument.** DF164 and DF168 were defects *in* a migrator, so a
sufficiently thorough migrator test suite is a plausible substitute for rehearsal. DF185 is not: it
is a composition defect across the schema gate, the migrator and the CLI's rendering, and no test of
any one of them can see it. Coverage that exercises the operator's sequence — not just the
migration — is therefore the requirement, which sharpens what §"What to build" has to assert.

**The specific thing to internalize:** DF164's failure printed `Data directory migrated
successfully` and exited 0. A test that runs `system migrate` and checks the exit code would have
passed. So would one that checks the schema stamp afterwards — the stamp was advanced, over
unconverted data. That is what the next section is about.

## What it has to prove

Ordered by how much each catches, not by how easy it is:

1. **The sandbox still works after migrating.** Not "migrate exited 0", not "the stamp advanced",
   not "the records parse" — start it, and get a diff back. This is the only assertion that would
   have caught DF164, because every weaker one passed while it was live.
2. **`--check` succeeds and describes the same work.** DF168 lived in the preview path, which is
   what a cautious user runs first, and it failed there for realms the apply path would also have
   refused. A dry run that errors is a bug even when the apply that follows works.
3. **The records are where the current binary looks for them.** Cheap, and it localizes a failure
   the "still works" assertion would only report as a mysterious breakage.
4. **The old binary refuses the migrated dir.** One direction only, and asserting it makes the
   downgrade story a fact rather than a claim — `config.PriorReleaseRange` exists to name concrete
   versions to fall back to, and nothing checks that the range is true.

## Shape

**Two binaries, one isolated data dir.** The harness today has exactly one binary
(`ctx.yoloai_bin`) and runs against the real `~/.yoloai`. Neither survives contact with this
scenario: it needs a *released* binary as well as the build under test, and it must not migrate the
developer's own data directory. `--data-dir` already gives the isolation, and every yoloai
subcommand takes it.

**Getting the old binary is the one genuinely new mechanism.** Two options, and the choice is a
cost/fidelity trade the owner should make:

- **Build from the tag** (`git worktree add <dir> vX.Y.Z && go build`). Hermetic, works offline,
  no network dependency in the gate — but it compiles an old tree with the *current* toolchain,
  which is a fidelity gap that will eventually bite (an old tree that no longer builds is itself a
  finding, but a confusing one to hit inside an upgrade test).
- **Download the release asset.** Tests the actual shipped artifact, which is the thing users have,
  and costs a network fetch plus a cache. GoReleaser publishes them, so they exist.

**One backend per platform is enough, and they should be the cheap ones.** docker on Linux,
seatbelt on macOS — seatbelt especially, because it needs no container runtime at all, which makes
it the fastest place in the whole matrix to exercise a migration. The migration is host-side file
movement; it is not per-backend behaviour, and paying for a tart VM to test it buys nothing. The
exception is the **running-sandbox refusal**, which *is* per-backend and belongs wherever a live
instance is cheapest.

**Where it runs** is the open question below. It is slower than any existing scenario (an extra
binary, possibly an extra base image) and it protects a rare event, which argues for the release
gate rather than the per-change tier.

## What it costs, honestly

The rehearsals that found DF164 and DF168 each took several minutes, dominated by one thing: a
released binary embeds *its own* `yoloai-base` Dockerfile and rebuilds the image on first use. Two
binaries therefore means two base images and two builds, on a host where that is ~90 seconds each
and where the images are large. Seatbelt sidesteps this entirely, which is a second reason to
prefer it on macOS.

This cost is why the scenario probably does not belong in `smoketest-quick`, and why a version
matrix wider than two entries needs an argument rather than an assumption.

## How far back to test, and why the answer is not "all of them"

The ladder's rungs are not equally interesting, and the interesting one is not the newest:

- A **v0.9.0** install (schema 5, records v3) exercises only the newest rung. It is the upgrade
  almost every real user will perform, and the cheapest to set up.
- A **v0.5.2** install (schema 2, records v1) exercises **all three** pre-v6 migrators in one run,
  including a live `:overlay` capture. That is the configuration that found both defects, and it is
  the one the register's [standing consideration](../../deprecations.md) is about retiring.

Those two are the plan's recommendation. Everything between them adds runtime without adding a
distinct code path. **Note the interaction with retirement:** if the owner raises the ladder floor,
the v0.5.2 case goes away with it — which is an argument for building this *before* deciding the
retirement, so the decision is made against a working test rather than against silence.

## Traps already paid for, in cash

Each of these cost real time on the branch and would cost it again:

- **A sandbox created by an unreleased build is not a migration case.** Branch builds write a
  partial `host/` tier, which no release produces; `TierLayout` refuses them by design. The test's
  fixture must come from a *released* binary, which is the whole point, but it means the scenario
  cannot be built by pointing the current binary at an old-shaped directory it made itself.
- **Old binaries refuse a newer schema.** The direction is one-way, so a test that reuses a data
  dir across cases must recreate it, not rewind it.
- **A migrated sandbox's running container still holds mounts into the displaced tree.** That is
  why the migrator refuses a running sandbox at all; a test that starts a sandbox, migrates, and
  then asserts against the *old* container is asserting against a ghost. Stop it first, as the
  migrator now demands.
- **`assert.Error` is not evidence of a denial** (A22). If any assertion here is negative — "the
  old binary refuses" — it needs to confirm the refusal is the *expected* one, and it wants a
  positive control beside it.

## Open questions for the owner

1. **Where does it run** — a new `smoketest` scenario (runs often, slows every smoke), or a
   `releasetest`-only tier (runs at the gate, which is when a migration actually ships)? Its subject
   is a rare event and its cost is high, which argues for the latter; the counter-argument is that
   the gate is exactly when discovering a broken migrator is most expensive.
2. **Tag-built or downloaded binary** — hermetic versus faithful, per § Shape.
3. **How many versions back** — the two recommended above, or only the newest, accepting that the
   configuration which found both defects then goes untested.
4. **Does it assert the failure paths too** (a running sandbox refused, a blocked sandbox refused)?
   They are cheap once the fixture machinery exists, and they are the paths where a wrong answer is
   silent rather than loud.

## Related

- [DF164](../findings-unresolved.md), [DF168](../findings-resolved.md) — the two defects this would
  have caught, and the reason the plan states its assertions in that order.
- [migration-by-duplication.md](../../archive/plans/migration-by-duplication.md) — the mechanism
  under test; its § What the build changed records what building it taught.
- [deprecations.md](../../deprecations.md) — the ladder's rungs and the standing question of
  retiring the oldest, which this plan's version matrix interacts with directly.
- [standards/go.md](../../standards/go.md) § A migrator addresses the layout of its own era — the
  rule DF164 graduated into, which this coverage exists to keep true.
