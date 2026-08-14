> **ABOUTME:** Stop merging configuration eagerly at each boundary. Parse every source into a
> provenance-tagged layer, carry the layers to one resolver, and apply a declared per-key policy —
> so "who supplied this value?" survives to the point where it decides something.

# Config provenance layers — one resolver, one policy table

- **Status:** PLANNED — designed, no code.
- **Depends on:** —
- **Blocks:** `repo-request-trust.md` (D141), `network-mode-reshape.md` step 1. Both need provenance;
  building them first means building it twice, in two places, badly.
- **Rides:** **any.** No user-visible change if the policy table is faithful to today's precedence.
  See [D143](../../decisions/working-notes.md).

## Why

Every configuration boundary in yoloAI collapses its inputs and discards which one won:

| Where | What is lost |
| --- | --- |
| `Coalesce(FlagStr(cmd, "isolation"), cfgIsolation)` — `cli/lifecycle/new.go:544` | flag vs personal config |
| `MergeProfileChain(layout, base, chain)` — `config/profile.go:493` | which layer set a field |
| `opts.Ports = append(merged.Ports, opts.Ports...)` — `create/prepare_profile.go:141` | which element came from where |
| `mergeDcMounts(pr, dcMounts)` — `create/create.go:518` | operator-authored vs repo-derived |

Nine findings from one audit are the same missing answer: DF196, DF197, DF205, DF206, DF207,
DF208, DF209, plus D141's whole subject. **Four of four `MergeProfileChain` callers passed the wrong
base** — a 100% failure rate against a guarantee `config.md:165,167` states in bold, which is the
measurement that says this is architectural rather than careless.

## Shape

**Layers, innermost first.** Each source is *parsed into* a common layer shape carrying only the
keys it can express — sources are not the same shape, and translating at the edge is what keeps the
resolver from knowing what a devcontainer is.

1. baked-in defaults (`config.DefaultConfigYAML`)
2. user defaults (`~/.yoloai/defaults/config.yaml`)
3. profile chain (root → leaf)
4. repo (`devcontainer.json`, translated)
5. library caller (`SandboxCreateOptions`)
6. CLI flags

**One resolver, one declared per-key policy.** Layers supply inputs; a single resolver applies the
table. If each consumer resolved for itself, the 4-of-4 divergence returns with better inputs.

| Merge kind | Keys |
| --- | --- |
| additive | `ports`, `network.allow`, `directories`, `cap_add`, `devices`, `setup` |
| replace | `mounts`, `agent_files`, `isolation`, `model`, `agent`, `os` |
| map-merge | `env`, `agent_args` |
| per-field | `resources` |

That table exists today only as the implicit order of assignments across three files, which is why
reconstructing it took a full audit. Written down, it is testable and diffable.

**Layer rules, expressed once.** The rule that pays for the work:

> when a profile layer is present, drop the user-defaults layer.

One line, replacing a per-key re-implementation at four call sites — which is how DF207, DF208 and
DF209 happened.

## Constraints

- **Presence must be explicit.** A layer distinguishes "did not set this key" from "set it empty",
  so keys need pointers or an option type. This also resolves a live ambiguity: `isolation: ""` is
  a *meaningful sentinel* meaning "ask the backend" (`runtime/isomode.go:19-22`), so the zero value
  already does double duty today.
- **Provenance is per-element for additive keys.** "Who contributed this port" is not a per-key
  fact. This is why `baseAgent`'s scalar comparison trick cannot be generalised.
- **It is invisible when it works**, so tests must pin the *policy*, not one path's outcome.

## Order of work

1. **Write the policy table as a test first**, asserting today's precedence for every key, against
   the current code. It must pass before anything moves — it is the safety net that says the
   refactor changed nothing except the listed defects.
2. **Introduce the layer type and explicit presence**, with translators at each edge. No consumer
   changes yet; the existing merge keeps running.
3. **Introduce the resolver** behind the existing signatures, so `MergeProfileChain` and friends
   become thin wrappers. Step 1's table must still pass.
4. **Move the layer rules in** — profile-drops-user-defaults first, since it closes DF207/DF208's
   residue and DF209.
5. **Delete the ad-hoc provenance mechanisms**: `IsolationExplicit` (`state.go:62`, DF205),
   the `baseAgent` comparison (`prepare_profile.go:99`), the
   `opts.Network == NetworkModeDefault` gates (`prepare_profile.go:144,208`, DF206).
6. **Expose provenance to consumers**, unblocking D141 (operator-authored vs repo-derived) and the
   network validation (typed vs inherited ports).

## Tests (rule 10)

- The policy table from step 1, per key, red on any precedence change.
- `isolation` and `model`: a personal default does **not** override a profile's (DF209, red on
  revert against today's code).
- The three retired mechanisms each lose their special case without behaviour changing.
- Provenance is queryable: a port from `--port`, a port from a profile, and a port from
  `devcontainer.json` are distinguishable at the resolver's output — the assertion D141 and the
  network validation both build on.

## Open

- **Does provenance persist?** `environment.json` records values, not sources. D141's consent copy
  needs the repo's contribution distinguishable at restart; whether that comes from the stored copy
  (per D141) or from a persisted source tag is not settled here.
- **Is `SandboxCreateOptions` a layer or the caller's whole request?** It currently mixes both —
  identity fields (`Name`, `Workdir`) alongside config-shaped ones (`Ports`, `Network`).
- **Does the CLI stop coalescing, or pass both values?** DF209 needs the flag and the config value
  to remain distinguishable past `new.go:544`; either shape works, and the choice affects how much
  of `cliutil` moves.

## Surfaces to sweep (rule 2)

- **Code:** `internal/config/{config,profile,defaults}.go`, `internal/orchestrator/create/{create,
  prepare_profile,prepare_archetype}.go`, `internal/orchestrator/lifecycle/{restart,lifecycle}.go`,
  `internal/cli/{lifecycle,cliutil}/`, `sandbox_options.go`, `profile_config.go`.
- **Contributor docs:** `design/config.md` (the precedence prose becomes a pointer to the table),
  `architecture/code-map.md` (gated), `principles/general-principles.md`.
