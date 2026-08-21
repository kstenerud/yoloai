> **ABOUTME:** Stop merging configuration eagerly at each boundary. Parse every source into a
> provenance-tagged layer, carry the layers to one resolver, and apply a declared per-key policy —
> so "who supplied this value?" survives to the point where it decides something.

# Config provenance layers — one resolver, one policy table

- **Status:** PLANNED — designed, no code. **Deferred to v0.13.0 per the 2026-08-15 audit of D143** (D143's Status now
  splits the decision into a repair that stands and a reversal left undecided; this plan builds the
  whole layer refactor, so it moves with the undecided half rather than starting early on the part
  that stands).
- **Depends on:** —
- **Blocks:** `repo-request-trust.md` (D141), `network-mode-reshape.md` step 1. Both need provenance;
  building them first means building it twice, in two places, badly.
- **Rides:** **breaking**, in **v0.13.0** — the caller layer must express *unset*, and that reaches
  the public `SandboxCreateOptions`. See "Expressing unset" below, and [D143](../../decisions/working-notes.md).

## Release scoping

**This plan does not ship in v0.12.0.** An earlier revision made it that release's headline; the
2026-08-15 audit of D143 moved it. v0.12.0 keeps the config/trust fixes that already landed
(`.yoloai.yaml` removed — D140; `mounts:` retired and `directories:` generalised — D142; the profile
merge-base leak closed on both paths — DF207/DF208), and closes the remaining findings **directly**
rather than as consequences of a refactor.

That reversal is the substantive change, so it is stated rather than quietly dropped: this plan
previously claimed DF205, DF206, DF209 and DF210 would close *as consequences* of the layer model.
They do not need it. **DF205** deletes a field with zero readers. **DF206** splits one over-broad
gate. **DF209** needs provenance for two scalars, which is a matched-inputs fix (see DF213 for why
the existing mechanism it named does not work). **DF210** applies or rejects one key. Four small,
independent fixes, none of which requires a resolver.

What the layer model is still *for* is the consumers that need **per-element** provenance —
`repo-request-trust.md` (D141) and `network-mode-reshape.md` — and both are themselves deferred to
v0.13.0. Building the resolver alongside them means building it against known consumers instead of
speculative ones. The migration rides with `network-mode-reshape.md`, so **no migration lands in
v0.12.0**.

## Expressing unset

The caller layer needs "did not set" distinguishable from "set to the zero value". Two shapes, and
the choice decides whether v0.12.0 breaks the library surface:

1. **Named sentinel values** — a reserved value meaning *unset*, per key whose zero value is already
   meaningful (`isolation: ""` means "ask the backend"; `network: ""` means open). No struct change,
   and it matches the totality invariant: absence is never how anything is expressed.
2. **Pointers or an option type** on the affected fields of `SandboxCreateOptions`. Unambiguous, and
   a public API break for every integrator.

Prefer (1); reach for (2) only where no value can be reserved. Whichever is chosen, `nil`-vs-empty
is already distinguishable for the slice and map keys, so this question is confined to scalars.

## Why

Every configuration boundary in yoloAI collapses its inputs and discards which one won:

| Where | What is lost |
| --- | --- |
| `Coalesce(FlagStr(cmd, "isolation"), cfgIsolation)` — `cli/lifecycle/new.go:544` | flag vs personal config |
| `MergeProfileChain(layout, base, chain)` — `config/profile.go:493` | which layer set a field |
| `opts.Ports = append(merged.Ports, opts.Ports...)` — `create/prepare_profile.go:151` | which element came from where |
| `mergeDcMounts(pr, dcMounts)` — `create/create.go:518` | operator-authored vs repo-derived |

Nine findings from one audit are the same missing answer: DF196, DF197, DF205, DF206, DF207,
DF208, DF209, plus D141's whole subject. **Four of seven `MergeProfileChain` callers passed the wrong
base**, against a guarantee `config.md:165,167` states in bold. The three correct ones live in the
repo-root `profile.go` and had been right since 2026-06-03 — which sharpens the diagnosis rather
than softening it: every wrong site was an orchestrator function that already had a `ycfg`/`cfg` in
scope, and every right one had to load something and loaded the right thing. **The trap is "a config
is already in hand"**, not the parameter's existence.

## Shape

**Four layers, innermost first.** Each source is *parsed into* a common layer shape carrying only
the keys it can express — sources are not the same shape, and translating at the edge is what keeps
the resolver from knowing what a devcontainer is.

| # | Layer | Loaded from |
| --- | --- | --- |
| 1 | baked-in defaults | `config.DefaultConfigYAML` — **must be total** |
| 2 | persistent preference | the named profile if there is one, **otherwise** `~/.yoloai/defaults/config.yaml` |
| 3 | repo | `devcontainer.json`, translated |
| 4 | caller | `SandboxCreateOptions` — the CLI included |

**Layer 2 has two mutually exclusive sources; it is not two layers.** "When a profile is present,
drop the user-defaults layer" then stops being a rule the resolver applies and becomes *how the
layer is loaded* — which is why it is worth insisting on. As a rule it is something callers
construct, and four of seven constructed it wrong; as a loader there is nothing to construct.

**The CLI is a caller, not a layer.** It already is one architecturally. What makes it look like a
layer is that it *also* reads the user's config — six sites (`cliutil/client.go:62,90,132,357,376`,
`lifecycle/new.go:538`) — while the library reads the same file at `create/create.go:485`. Every key
it resolves that way is broken (`model`, `isolation` — DF209), inert (`os` — DF210), worked around
(`agent` — and that workaround is itself broken, DF213), or handled by a different mechanism
(`backend`). The CLI stops reading
config and only translates flags into the caller layer; DF209 then cannot be represented.

**One resolver, one declared per-key policy.** Layers supply inputs; a single resolver applies the
table. If each consumer resolved for itself, that divergence returns with better inputs.

| Merge kind | Keys |
| --- | --- |
| additive | `ports`, `network.allow`, `directories`, `cap_add`, `devices`, `setup` |
| replace | `agent_files`, `isolation`, `model`, `agent`, `os` |
| map-merge | `env`, `agent_args` |
| per-field | `resources` |

That table exists today only as the implicit order of assignments across three files, which is why
reconstructing it took a full audit. Written down, it is testable and diffable.

**This table is known incomplete** — it predates D142 (which retired `mounts:`, removed above) and
was never checked against the current `YoloaiConfig` struct fields. At least `container_backend`,
`tart.image`, `auto_commit_interval`, `network.isolated`, `workdir` and `backend` have no row. It
must be rebuilt from the actual struct fields when this work is built, not trusted as written —
perfecting a table for work that is not yet in scope is not worth doing now.

**Totality invariant.** Layer 1 specifies every key, so resolution never falls off the end with no
value. Two keys break it today: `agent_files` is commented out of the shipped template
(`defaults.go:66`) — which is why `resolvedAgentFiles`' nil-fallback re-leaked the personal value in
DF208. The `isolation` half of this claim was **wrong**: `defaults.go:42` reads `isolation:
container`. The real gap is worse — there are **two loaders with different layer sets**
(`LoadDefaultsConfig` merges baked-in defaults, `LoadConfig` does not), so the no-profile create
path never applies layer 1 at all. Making layer 1 total *and always applied* would change
`isolation` from the backend base mode to `container` there, which is user-visible on seatbelt and
apple and must be costed.

**Outside the stack, deliberately.** The **agent definition** contributes a `NetworkAllowlist`
*floor* that `netpolicy.Compose` prepends after every layer and no layer may lower — different
semantics, so it sits beside the resolver. The **backend** resolves `isolation: ""` to its own base
mode (`process` on seatbelt, `vm` on apple) — a step after layering. Keeping both out is what stops
the resolver needing a special case for "this layer is different".

## Constraints

- **Presence must be explicit.** A layer distinguishes "did not set this key" from "set it empty",
  so keys need pointers or an option type. This also resolves a live ambiguity: `isolation: ""` is
  a *meaningful sentinel* meaning "ask the backend" (`runtime/isomode.go:19-22`), so the zero value
  already does double duty today.
- **Provenance is per-element for additive keys.** "Who contributed this port" is not a per-key
  fact — a different problem from `baseAgent`'s scalar comparison, which is separately broken
  (DF213). Note `netpolicy.WithProvenance` (`compose.go:7-50`, public at `network.go:16-31`) already
  does per-element provenance for `network.allow` by set membership, and its own docstring records
  the limitation: provenance "degrades gracefully to 'everything looks user-added'".
- **It is invisible when it works**, so tests must pin the *policy*, not one path's outcome.

## Order of work

1. **Write the policy table as a test first**, asserting today's precedence for every key, against
   the current code. It must pass before anything moves — it is the safety net that says the
   refactor changed nothing except the listed defects.
2. **Introduce the layer type and explicit presence**, with translators at each edge. No consumer
   changes yet; the existing merge keeps running.
3. **Introduce the resolver** behind the existing signatures, so `MergeProfileChain` and friends
   become thin wrappers. Step 1's table must still pass.
4. **Move the layer rules in** — profile-drops-user-defaults first. DF207/DF208/DF209 are already
   closed directly (see below), so what remains here is making the rule structural rather than a
   comparison.
5. ~~**Delete the ad-hoc provenance mechanisms.**~~ **Done ahead of this plan, 2026-08-15, and that
   changes what step 6 inherits.** `IsolationExplicit` is deleted (DF205 — it had zero readers), and
   the `opts.Network == NetworkModeDefault` gates are split so only the mode promotion is
   conditional (DF206). The `baseAgent` comparison **stays**: it was broken and is now fixed (DF213),
   and `model`/`isolation` were given the same treatment (DF209), so three keys now carry a working
   comparison that this plan proposes to replace with something structural. That is the honest state
   — not "the mechanisms are gone", but "they work, and a comparison is still standing in for
   provenance". The residual they all share is the argument for building this: **an explicit flag
   whose value equals the resolved default is indistinguishable from no flag.**
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
