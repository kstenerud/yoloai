# Procedures

How we work. The rules an agent must follow are in [`AGENTS.md`](../../../AGENTS.md); these
docs are the detail behind them.

| Doc | Covers |
| --- | --- |
| [`pull-requests.md`](pull-requests.md) | Branching, breaking changes, the name sweep, commits, the quality gate, CI jobs, required tooling, test tiers. |
| [`issues.md`](issues.md) | What an issue needs to be actionable. |

Releases are cut by the maintainer: `docs/BREAKING-CHANGES.md`'s `## Unreleased` section is
renamed to the version, then an annotated `vX.Y.Z` tag is pushed, which drives
`.github/workflows/release.yml`.
