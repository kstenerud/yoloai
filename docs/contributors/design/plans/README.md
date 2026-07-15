> **ABOUTME:** Routes to the live plans without listing them — how to find one, and the ordering
> between them that no single plan states. Every idea here is its own file; this document
> deliberately holds no inventory.

# Unfinished Work

Every plan for work that is designed, part-built, or merely wanted is a **file in this
directory** — an idea nobody has fleshed out yet is a plan with `**Status:** UNSPECIFIED`, not a
bullet in this README. A backlog kept as README prose is invisible to `ls`, to `grep`, and to
every gate, which is how finished items sat in this one unnoticed.

A plan states where it stands in a metadata list under its title:

| `**Status:**` | Means | Lives in |
| --- | --- | --- |
| `UNSPECIFIED` | An idea. No design yet. | here |
| `PLANNED` | Designed, not built. Includes parked work — why it is parked is prose in the plan, not a state. | here |
| `IN-PROGRESS` | Partly built. Some shipped, some didn't; the Status prose says what's left. | here |
| `IMPLEMENTED` | Done. Nothing remains. | [`../../archive/plans/`](../../archive/README.md) |
| `ABANDONED` | Decided against. | [`../../archive/plans/`](../../archive/README.md) |

**A finished plan leaves.** Once its work is done or dropped, a plan's only use is archaeological
— "did we consider X?", "why was X decided that way?" — which is what the archive is for. It moves
whole, in the same PR. `TestRepoHygiene_PlanStatus_IsDeclaredAndLive` fails if a plan here reads
`IMPLEMENTED` or `ABANDONED`, so the move is mechanical rather than something to remember.

`- **Depends on:**` names other live plans, or `—`. It is declared one way: "A depends on B" lives
in A, because A's author knows what A needs. The reverse view is a grep
(`grep -l 'Depends on:.*B.md' *.md`), which cannot go stale — see `standards/markdown.md`.

## Finding a plan

`ls docs/contributors/design/plans/` is the complete list of plans. `head -3 <file>` gives a
plan's purpose (its ABOUTME). `grep '^- \*\*Status:\*\*' *.md` shows where each stands. There is
no list of plans here, on purpose: an earlier version of this file drifted to describing 20 plans
while 29 existed, and a partial index answers "is there a plan for X?" with a false no.

## Sequence and dependencies

The part `ls` cannot give: what gates what, what must land first.

- `egress-proxy-build.md`'s next step is `tamper-resistant-network-isolation.md` (containment
  step 1.5).
- `retire-overlay-reflink-copy.md` is the active plan for the `:overlay` retirement (D109); the
  audit record (`overlay-sysadmin-escape.md`) is archived.
- `public-layering.md` is the frame; `move-audit.md` and `session-carve.md` are its sub-tasks.
- `agent-detection-strategies.md` was a merge gate for the `public-layering` branch; that plan is
  now archived, so the gate is satisfied.
- `podman-gvisor.md` and `setup-gvisor.md` are the two gVisor plans; `setup-gvisor.md` has a
  blocking decision (the OrbStack `/tmp` collision).
- `post-merge-roadmap.md` sequences the D99 post-merge remainder across many of these and carries
  the recommended order plus the decisions a human must make first. Its per-workstream status
  column is a copy of each plan's own and is known to lag (DF103) — trust the plan, not the table.
