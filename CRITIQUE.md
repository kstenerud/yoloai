# Critique — Round 5

Fifth design audit. Focused on profiles redesign, directory UX, and design gaps.

## Applied

- **C38.** Directory UX redesign — separated workdir (positional) from aux dirs (`-d` flag). Updated CLI syntax, all examples, profiles, and command listing.
- **C39.** Profiles redesign — moved profile configs out of `config.yaml` into individual `~/.yoloai/profiles/<name>/profile.yaml` files. Added `yoloai profile` subcommands (create/list/delete) with templates. Consolidated merge rules into dedicated Profiles section.
- **C40.** `yoloai start --resume` — added `--resume` flag that re-feeds original prompt with continuation preamble.
- **C41.** `yoloai tail` — replaced `yoloai log -f` with dedicated `yoloai tail` command.
- **C42.** `yoloai stop` agent behavior — documented SIGTERM behavior, agent state persistence, and stop/start semantics.
- **C43.** Credential management industry expectations — added subsection covering rotation, vault integration, OAuth gaps, and assessment that file-based injection is industry-standard for this level.
- **C44.** `--network-none` warning — added prominent warning that most agents need network access for API endpoints.
- **C45.** Config template generation — deferred to v2 as resolved design decision #10.
- **C46.** GOPATH example — replaced with `GOMODCACHE` and added clarifying comment in profile.yaml example.
- **C47.** `yoloai restart` documentation — added use cases (corrupted env, config changes, wedged agent).
- **C48.** `yoloai list` AGENT column — added AGENT column showing configured agent.

## Deferred

- **tmux alternatives research** — whether tmux is the right choice, competitors (zellij, screen), alternative approaches to session reconnection. Needs dedicated research before changing.
- **Network allowlist as firewall rules** — whether per-agent allowlists should be replaced with a more general firewall-rules approach. Needs research into UX implications.
- **Per-agent vs unified network rules** — whether agent-specific allowlists add complexity without benefit. Related to the firewall rules question above.
- **`yoloai build` timestamp-based rebuilds** — incremental rebuild based on file timestamps. Needs research into Docker layer caching interaction.
- **New competitors analysis** — HN thread (item 47113567) mentions new competitors. Needs research pass for RESEARCH.md.
