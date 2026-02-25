# Contributing to yoloai

## Building

```sh
make build
```

## Running Tests

Unit tests:
```sh
make test
```

Integration tests (requires Docker):
```sh
make integration
```

## Before Submitting a PR

Run the full check suite locally:
```sh
make check
```

This runs the same checks as CI: formatting, linting, `go mod tidy` verification, and tests. If `make check` passes locally, CI will pass too.

## Code Style

- Go code is formatted with `gofmt`
- Linting via `golangci-lint` (see `.golangci.yml` for config)
- See `docs/CODING-STANDARD.md` for project conventions

## Commit Style

One commit per logical change. Keep commits focused and self-contained.
