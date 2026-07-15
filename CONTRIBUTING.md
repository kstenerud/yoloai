# Contributing to yoloai

Thanks for contributing. Nearly all changes here are prepared with the help of a coding
agent, so the rules live in a file agents load automatically:

**Read [`AGENTS.md`](AGENTS.md) before you start.** It is short, and it is the contract.
The detail behind it is in
[`docs/contributors/procedures/pull-requests.md`](docs/contributors/procedures/pull-requests.md).

Two rules catch nearly everyone, so they are worth repeating here:

- **A user-visible break needs a `docs/BREAKING-CHANGES.md` entry, under `## Unreleased`, in
  the same PR as the code.**
- **If you rename or invalidate a name, sweep every surface that names it** — including the
  help topics under `internal/cli/helpcmd/help/`, which are compiled into the binary and are
  shipped UI. `make check` does not check any of this.

## Building

```sh
make build
```

## Running tests

Unit tests:

```sh
make test
```

Integration tests (requires Docker):

```sh
make integration
```

## Before submitting a PR

```sh
make check
```

`make check` is your local gate and it must pass. **It is not all of CI** — CI additionally
runs the `integration` and `integration-podman` jobs, and `make check`'s `test` target skips
every build-tagged test. It also cannot see docs or shipped help text. See
[`pull-requests.md`](docs/contributors/procedures/pull-requests.md) for the CI jobs, the
required tooling (`rsync`, `uv`, `hadolint`, `actionlint`, Docker), and the test tiers.

## Code style

- Go is formatted with `gofmt`; linting via `golangci-lint` (see `.golangci.yml`).
- [`docs/contributors/standards/`](docs/contributors/standards/) — per-technology conventions
  (`go.md`, `cli.md`, `shell.md`, `python.md`, …).
- [`docs/contributors/principles/`](docs/contributors/principles/) — the reasoning those
  standards ground out from. A principle wins over a conflicting standard.

## Commit style

One commit per logical change; keep commits focused and self-contained. Subject is
`type(scope): summary`, code commits carry a prose body, and AI-assisted commits name the
model in a `Co-Authored-By` trailer — see [`AGENTS.md`](AGENTS.md) for the full rules.
