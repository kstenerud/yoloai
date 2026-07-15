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

`make check` runs: `lint lint-cross vet-tagged crosscheck tidy-check hadolint actionlint test
python-test`.

**It is not all of CI.** CI runs three jobs on every PR:

| Job | Runs |
| --- | --- |
| `check` | `make setup-dev-python`, `make check` |
| `integration` | `make base-image`, `make integration`, `make e2e` |
| `integration-podman` | `make integration-podman` |

`make check`'s `test` target is a bare `go test ./...`, which skips every
`//go:build integration` and `//go:build e2e` file. A green `make check` does **not** predict
a green CI.

**What the gate cannot see:**

- **Prose.** Shipped help text, `docs/`, and every surface in rule 2. It stays green while the
  binary advertises a config key it rejects.
- **Build-tagged tests.** Verify type changes across them with
  `go vet -tags 'integration e2e' ./...` or `make releasetest`.

## Required tooling

Absent tooling **fails**; it does not skip (D112). Install all of it:

| Tool | Used by |
| --- | --- |
| `rsync` | in-place `reset` lifecycle tests |
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
