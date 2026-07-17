> **ABOUTME:** The detail and reasoning behind AGENTS.md's PR rules — why branch from main
> instead of a tag, what counts as a breaking change, how to sweep a renamed identifier across
> every surface that mirrors it, and what `make check` covers versus the rest of CI.

# Pull requests

The rules themselves are in [`AGENTS.md`](../../../AGENTS.md); this is the detail behind
them and the reasoning that makes them stick. Read `AGENTS.md` first.

## Start from an up-to-date `main`

Not from a release tag. A tag is a stale base by definition — you miss whatever landed since.

It also used to be actively dangerous, which is worth knowing because it explains a convention
you will meet. Releasing once *renamed* `## Unreleased` to the version being tagged, so a tag's
tree had no marker at all and the freshly-shipped section sat on top, reading exactly like the
open one. An entry filed there merged **cleanly** — no conflict to warn anyone. It happened
twice: once to an outside contributor, and once to a maintainer's agent that was fixing the
first instance. D117 made the marker permanent, so that trap is closed even from a bad base.
Branch from an up-to-date `main` regardless.

## 1. Breaking changes

**What counts:** anything that used to work and now doesn't. Removed or renamed flags and
config keys, changed defaults, new validation rejecting previously-accepted input, changed
output shape that scripts parse.

**Where:** `docs/BREAKING-CHANGES.md`, under `## Unreleased`, in the same PR as the code.
Never under a `## vX.Y.Z` heading — a version heading means shipped and frozen. The
`## Unreleased` marker is always present, so it is always where you need it; releasing drains
its entries into a new version heading beneath it rather than renaming it away.

**Shape:** previous behaviour, new behaviour, impact, migration. Copy the surrounding entries.

**Why it's rule 1:** the first outside PR to this repo (#36) made `yoloai config set backend
docker` exit 1 — a hard break — across nine changed files, none of them
`docs/BREAKING-CHANGES.md`. The entry only existed because the maintainer asked for it in
review. Nothing in the repo told the contributor it was needed.

**Half of it is gated now, on the PR.** `scripts/check_breaking_changes.py` (a `commits` job
step, since it needs the base ref) compares the set of user-visible names at the base against
HEAD and fails when one **disappears** — a removed or renamed config key or CLI flag — with no
`BREAKING-CHANGES.md` entry in the same change. It is a set difference, so a name that merely
moves between files is a non-event. Measured before landing: 0 firings across 51 real merges,
and it blocks a synthetic `container_backend` or `--debug` rename.

It sees **removals only**. A changed default or newly-rejected input is still rule 1 and still
review-caught — a partial gate on the recurring case beats none on all of them. Note the other
BREAKING-CHANGES check, in `release.yml`, is not this: it fires on a release tag and asserts
`## Unreleased` is *empty*, which proves entries were drained and cannot see one that was never
written. It passes most loudly in exactly the failure case, which is why this exists.

**The other half: a break you chose to defer is a deprecation, and it gets registered.** If
instead of breaking you kept the old form working — a migration, a legacy reader, an alias — you
did not avoid the break, you scheduled it. Add it to
[`../deprecations.md`](../deprecations.md) with the date you incurred it and a due date (D127);
`make check` gates the format, not the date. A migration counts: writing one commits the project
to the old form from that day. The register's own audit found 16 such mechanisms and **0**
recording a date, which is how "we'll clean it up later" reads after a year.

**Retiring one is a plain rule-1 break**: `## Unreleased` entry, delete the register entry, done.
The register is the staging area — *registered → settles → retired → recorded here*. When you cut
a release, anything past its due date is a retire-or-extend call for the owner; nothing is
automatic.

## 2. The name sweep — the surfaces

A name the Go code owns is mirrored verbatim into text that no compiler, linter, or test
validates. When you rename or invalidate one, `git grep` the old name tree-wide and fix
every **live** mention:

| Surface | Note |
| --- | --- |
| `internal/cli/helpcmd/help/*.md` | `//go:embed help/*.md` (`help.go`) — **shipped UI**, despite `internal/`. Wrap at 80 columns (`standards/cli.md`). |
| cobra `Short`/`Long` strings | `internal/cli/**` |
| `docs/GUIDE.md` | tables *and* prose examples |
| `README.md`, `docs/contributors/**` | contributor docs count |

**Check every block within a file, not just the first hit.** A help topic's settings table
and its examples drift independently — PR #36 fixed the examples and left the table naming
the dead key 13 lines above.

**Append-only history is exempt:** `docs/BREAKING-CHANGES.md`, `docs/contributors/archive/**`,
`decisions/**`, and the `*-resolved.md` / `*-deferred.md` / `*-abandoned.md` sinks record what
was true at the time and correctly still name dead identifiers. Exempt from the *sweep* is not
exempt from rule 1.

**Why it matters:** `backend` was renamed to `container_backend` in March 2026. The help topic
kept advertising `backend` through **all 15 releases from v0.2.0 to v0.8.0** — four months of
shipping a key the binary rejects — with `make check` green the whole time. A human caught it,
not a test.

## The quality gate

```sh
make check
```

For what it runs, read the `check:` line in the Makefile. It is not copied here on purpose: the
list has grown four times and every pasted copy of it in this repo has been stale at some point
(D121).

One target is worth knowing by name, because its failure is the least self-explanatory:
`lint-speculative-api` fails on a declaration whose only callers are tests (D125). The normal
lint cannot see that class — `unused` counts a test caller as a caller — so this is the only
thing standing between the tree and API that nothing calls.

**It is not all of CI.** CI runs three jobs on every PR:

| Job | Runs |
| --- | --- |
| `check` | `make setup-dev-python`, `make check` |
| `commits` | `scripts/lint_commits.py` over your PR's commits (PR events only) |
| `integration` | `make base-image`, `make integration`, `make e2e` |
| `integration-podman` | `make integration-podman` |

`make check`'s `test` target is a bare `go test ./...`, which skips every
`//go:build integration` and `//go:build e2e` file. A green `make check` does **not** predict
a green CI.

**Commit messages are checked in CI, not by `make check`** — the check needs a base ref
to diff against, which `make check` has no notion of. Run it yourself before pushing:

```sh
make lint-commits              # against origin/main
make lint-commits BASE=upstream/main
```

**What the gate cannot see:**

- **Prose.** Shipped help text, `docs/`, and every surface in rule 2. It stays green while the
  binary advertises a config key it rejects.
- **Build-tagged tests.** Verify type changes across them with
  `go vet -tags 'integration e2e' ./...` or `make releasetest`.

## Required tooling

Absent tooling **fails**; it does not skip (D112). Install all of it:

| Tool | Used by |
| --- | --- |
| `uv` | the Python surface (`python-test`, `python-typecheck`) |
| `hadolint` | Dockerfile lint |
| `actionlint` | workflow lint |
| Docker | integration tiers |

The one carve-out is `YOLOAI_TEST_UNCONTROLLED_BACKENDS`, a comma-separated list of backends
to exclude when the host genuinely cannot run them. CI uses it for containerd (GitHub runners
have no nested KVM).

## Test tiers are infra-gated

The integration tiers cannot be one command: containerd/Kata netns setup requires root, while
other targets hard-error *under* root. CI is the authority on them — if you can't run a tier
locally, say so in the PR and let CI decide.
