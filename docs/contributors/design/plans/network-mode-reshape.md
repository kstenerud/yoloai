> **ABOUTME:** Replace the two network booleans with one four-valued `--network` flag whose values
> name *where the enforcement machinery sits relative to the agent* — and make every mode balk
> loudly at the features it cannot honour, instead of degrading.

# Network mode reshape — one flag, four trust boundaries

- **Status:** PLANNED — designed, no code.
- **Depends on:** egress-proxy-build.md

  Only step 4 needs it — steps 1–3 depend on nothing.
- **Rides:** **a migration.** See § *Why this is migration-bearing* — a released binary reading a
  `restricted` record does not error, it silently produces an unisolated sandbox. Per rule 12 /
  [D131](../../decisions/working-notes.md) the work goes to `release-v0.12.0` and **not to `main`**.

## The axis

Two booleans could not say the thing this workstream spent a month establishing. One flag, four
values, and **the axis is where the enforcement machinery sits relative to the agent** — which is
the only thing that decides what a mode is worth against an agent that turns on you.

| Mode | Machinery sits | Guarantee |
| --- | --- | --- |
| `open` | nowhere | none. Anything in the sandbox reaches anything. |
| `isolated` | **inside** the sandbox | restricts egress. **No guarantee against an agent subverting the machinery itself.** |
| `restricted` | **outside** the sandbox | restricts egress, hardened against a compromised agent. |
| `none` | — | no network at all. Nothing in or out. |

**Each value names a trust boundary, not a mechanism.** `ip-filter` and `egress-proxy` remain
strategies underneath, so [netpolicy.md](../netpolicy.md)'s mode-is-intent / strategy-is-realization
split survives — but **one sentence of it does not, and this plan supersedes that sentence rather
than continuing it.** netpolicy.md §3 decided this exact question: *"'Hostile' = `isolated` + the
`egress-proxy` strategy — a new **strategy**, not a new **mode**."* `restricted` is that
combination, promoted to a mode.

The reason to overturn it is that its premise changed. It was written when the proxy was expected to
be *"a strictly-better interpretation of the same allowlist… with no policy-model change and no
contract change"*. [D137](../../decisions/working-notes.md) and the chokepoint round make it
neither: `--port` stops working, live `allow`/`deny` stops working, brokering becomes mandatory, and
the policy set must be fixed at creation. **A strategy that changes the contract is a mode.** An
earlier draft of this plan claimed the carve was untouched and cited a neighbouring section as
endorsement — the supersession-as-continuity failure [A38](../../agent-failures.md) records,
recurring inside the plan that records it.

The ordering falls out of the axis rather than out of the adjectives, which is why the values can be
read cold: machinery nowhere, inside, outside, no network.

### Two rungs deliberately collapsed

`isolated` covers what [D135](../../decisions/working-notes.md) called two tiers — in-guest
filtering, and in-guest filtering the agent cannot flush (the sidecar firewall). They are one mode
now. The distinction was an artifact of believing a sandbox's inner machinery could be hardened
against a compromised agent; [D137](../../decisions/working-notes.md) measured that it cannot,
because the agent has `sudo` by design and the residue behind that is unbounded. **A better tier 1
is not a different guarantee, so it does not get its own name.** Ideas from the sidecar work may
still make `isolated` more robust and are worth taking where cheap — **not worth new privilege
requirements**, and never worth being described as containment.

## What each mode balks at

**Degrading is retired** ([D138](../../decisions/working-notes.md)). A mode that cannot be delivered
is refused; a feature the mode cannot honour is refused. The point of the axis is that a user can
hold the guarantee in their head, and a guarantee that quietly varies by host — or that silently
drops a feature they asked for — is not one.

### `none` — the sandbox is air-gapped

No agent can run here: every shipped agent needs its API server, so **no API access means no use for
a key and no purpose for a broker.** The mode is for running untrusted *code* — a suspicious
repository, a dependency's test suite, a build script nobody has read — with `--agent idle` and
`exec`, where copy/diff/apply is the point.

| Requested | Behaviour | State today |
| --- | --- | --- |
| a real agent (`agent.RealAgents()`) | **refuse** | accepted; the agent then sits unable to work |
| `--network-allow` or a profile allowlist | **refuse** | silently discarded ([DF196](../findings-unresolved.md)) |
| `--port` | **refuse** | refused at the CLI only; a profile's `ports:` slips past ([DF197](../findings-unresolved.md)) |
| `--broker` | **refuse** | already refused |
| credential delivery | **deliver nothing** | falls back to *direct* delivery — the real key enters an air-gapped box for no reason |
| MCP servers needing egress | **refuse** | undefined |

The predicate for the first row is `agent.RealAgents()`, which already excludes `test`, `shell` and
`idle`. **It is not "the agent's allowlist floor is non-empty"** — `aider` ships with five
`APIKeyEnvVars` and *no* `NetworkAllowlist`, as does any file-defined agent that omits the optional
field, so a floor-based rule would wave through the agent most likely to fail silently.

The credential row reads as a surprise and is not: refusing the broker currently *causes* direct
delivery, so `none` has **worse** credential hygiene than `isolated` today. Under this plan it has
none, because there is nothing for a credential to reach.

### `restricted` — one route out, and it is the proxy

All routes are removed and a single host-side proxy is the only destination
([D139](../../decisions/working-notes.md)). Several network features cannot survive that. Each is a
deliberate compromise for the guarantee, not an oversight:

| Requested | Behaviour | Why |
| --- | --- | --- |
| `--port` | **refuse** | a guest with no route cannot answer inbound. A **contract property of the mode**, not of the mechanism — a backend falling back to shape (A) could technically publish and must not, or the mode means different things per backend |
| live `allow` / `deny` | **refuse** | D137 §2 requires the policy set fixed at creation; a runtime-mutable allowlist is the open-ended set moving rather than closing |
| brokering | **mandatory** | the single destination *is* the broker's injector. A credential must not enter a sandbox whose whole premise is that it cannot be trusted with one |
| MCP servers needing arbitrary egress | **refuse** | they would need a second route |

### `isolated` and `open` — unchanged behaviour

`open` promises nothing. `isolated` works as it does today; what changes is that it is *described*
honestly, and that it is refused rather than silently unenforced where a backend cannot do it.

## Why this is migration-bearing

`internal/netpolicycfg` stamps `schemaVersion = 1` on save and **never compares it on load** —
`Load` unmarshals `Version` and returns. The constant is unexported, so nothing outside the package
could check it either. Compare `store/environment.go:333`, which guards both directions.

So a **released v0.11.0 binary reading `"network_mode": "restricted"` does not fail.** It falls
through every `== "isolated"` test and produces a sandbox with no isolation at all: no sidecar
firewall (`launch.go:301`), no `NET_ADMIN` (`:1040`), `CanEnforce` never consulted (`:990`), and the
guest firewall off, because `entrypoint.py` keys on a bool rather than on the mode string. On
seatbelt and apple the guest simply gets full network. Only docker errors, and only by luck — it
passes the mode through as a *network name* and the daemon rejects it.

Patching `Load` does not fix this: it would protect binaries from that release forward, and the
stranded population is everything already shipped. The clean refusal has to come from the realm
stamp (`LibrarySchemaVersion`), which means a registered migrator, a `schema_releases.go` entry, a
rule 9 deprecation entry — and rule 12: **`release-v0.12.0`, not `main`.**

## Failing loudly

Each refusal is newly-rejected input: a `BREAKING-CHANGES.md` entry and a test that goes red on
revert (rule 10).

**These are not per-flag guards. They are one validation the product does not have.**

The tempting fix is a check per flag, and it is wrong twice over. It would be the fourth mechanism
for one job (GEN §18), and it would sit in the layer that the door most worth defending does not
pass through: **the library**. `Client.CreateSandbox` → `SandboxCreateOptions.toInternal()` →
`Engine.Create` never enters `internal/cli`, and `toInternal` is a pure field copy — it carries
`Network`, `NetworkAllow` and `Ports` across verbatim (`sandbox_options.go:165-167`) and nothing
validates any of them afterwards. So an integrator can ask for `none` with an allowlist and ports
today and be told nothing. A profile is a third door, and archetypes a fourth for ports.

**Where it goes, and it is earlier than it looks.** Everything the check needs — `opts.Network`,
`opts.NetworkAllow`, `opts.Ports`, `opts.Agent` — is **final at the end of Phase 1**
(`resolveProfileAndArchetype`, `create.go:237`); the last writes are `prepare_profile.go:141`,
`:199`, `:146`, `:208`, `:99` and `prepare_archetype.go:283`, and nothing writes them afterwards.
`buildNetworkConfig` (`prepare_dirs.go:341`) is a pure function of those, so the resolved mode is
available immediately.

**So the check goes right after `create.go:240` — before `replaceSandboxIfNeeded`.** That ordering
is not cosmetic: `replaceSandboxIfNeeded` (`:242`) calls `launch.Teardown` and **destroys the user's
existing sandbox**, and Phase 2 (`:253`, `:266`) copies the whole workdir. Validating after Phase 3,
as an earlier draft of this plan said, would mean `yoloai new box . --replace --network=none
--port 3000` tears down a working sandbox, copies the tree, and *then* refuses. The `defer
os.RemoveAll` cleans up the new directory; nothing restores the old one.

**One caveat that decides where the code cannot go:** `prepareSandboxState` takes `opts` **by
value** (`create.go:230`). The merged ports exist only inside it — a validator in `create.Run` or
`Engine.Create` would read pre-merge values and be silently wrong.

**`Compose` takes the user-allowlist half and only that.** It has one call site
(`prepare_dirs.go:342`), so refusing there means the discard cannot happen even if the outer check
is bypassed. But it must refuse on a non-empty **`userAllow`** *only* — it receives `agentFloor` as
a list and cannot see *which* agent produced it, so a floor-based refusal there would kill
`--agent claude --network=none` inside `Compose`, make the outer agent check dead code, **and still
wave `aider` through**, since aider has no floor. The agent predicate belongs in the outer
validator, where `opts.Agent` and `agentDef` are both in scope.

**The broker is not in this validation at all**, and cannot be. `create.Options` has no broker
field: `SandboxCreateOptions.Broker`/`NoBroker` exist but `toInternal()` drops them
(`sandbox_options.go:145-181`), the CLI routes them through `SandboxStartOptions` instead, they are
persisted to meta at **start** (`start.go:99`), and they are consumed at **launch**
(`brokerCredentials`, `launch.go:592`). `restricted`'s "brokering is mandatory" is also unevaluable
at create — it depends on `resolveBrokerReach`, which needs a live backend. So **the broker and
credential-delivery rows of both tables are launch-side work**, sitting beside the existing refusal
at `launch.go:613`, and they are not part of step 1.

The runtime path is the model to copy: `Network.Allow` → `requireIsolated` refuses on a `none`
sandbox regardless of how the request arrived, because it validates against the **stored mode**
rather than against the flag that produced it. The existing `--port` check is the counter-example —
`new.go:207` reads `cmd.Flags().GetStringSlice("port")`, so every non-flag door slips past
([DF197](../findings-unresolved.md)).

**One correction to an earlier draft, because it changes what the argument rests on:** a profile has
no mode key at all. `config.NetworkConfig` is `Isolated bool` + `Allow []string`, so a profile can
supply the allowlist half but never `none`. The conclusion survives — but four modes mean a **new
config key**, which is a rule-1 rename carrying a 12-month user-facing deprecation, a real parser
(`handleYoloaiNetwork` validates nothing today), and edits to the shipped default config and the
profile scaffold.

**MCP reaches none of this.** No tool accepts a network mode or ports (`internal/mcpsrv/tools.go`),
and `sandbox_status` drops `network_mode`. Not a hole today — an MCP caller cannot request a mode it
cannot express — but it means the four-mode story does not reach that surface, and `restricted` will
be unrequestable there until it does.

## What must be verified before this ships

**`--network-none` is not honoured on containerd, apple or tart.** containerd says so in its own
comment (*"the `runtime.NetworkMode == "none"` CLI flag is not currently honored… setupCNI is
unconditional"*); apple reads the mode only for `"isolated"`; tart has no `NetworkMode` handling at
all. docker and podman honour it natively, and seatbelt omits `(allow network*)`.

Shipped help says *"it holds on every backend"* and `netpolicy.md` says *"`none` is a hard boundary
on every backend"*. Both are wrong on half the backends, both are security claims in user-facing
text, and this is a **live defect independent of this plan** — filed as
[DF198](../findings-unresolved.md). Static reading only: the containerd half is verifiable on the
Linux host, apple and tart need the Mac. **Correct the text first, then the backends** — and note
that under D138 an unenforceable `none` must be *refused*, which needs a `BackendCaps` field that
does not exist (`runtime.go:284` carries only `NetworkIsolation bool`).

## What this does not decide

- **git over SSH under `restricted`** — it works through a `CONNECT` tunnel, no tunnel tool ships in
  the image, and allowing it hands the proxy an opaque stream. Owner's call, recorded in D137.
- **The proxy's policy language.** D137 §2 requires it closed at creation; the spelling belongs to
  `egress-proxy-build.md`.
- **Where host-side *allowlist* enforcement lands** (`enforcement-build.md`, D135's old tier 3). It
  is out-of-sandbox machinery, so it belongs on `restricted`'s strategy axis rather than being a
  fifth mode — but whether it survives D137 at all is not settled here.
- **`restricted` and D133.** D133 decided allowlisted domains resolve in the guest's resolver
  context, never on the host; under `restricted` the proxy resolves host-side. Whether D133 narrows
  to `isolated` or is contradicted needs stating — and D133 already carries its own audit note
  calling itself provisional.

## Order of work

1. **One mode-capability validation in `prepareSandboxState`**, after `create.go:240` and before
   the destructive replace, covering **allowlist, ports and agent** — plus `Compose` refusing a
   non-empty user allowlist at its own layer. Closes [DF196](../findings-unresolved.md) and
   [DF197](../findings-unresolved.md) together; they are two symptoms of this step being absent, so
   fixing them separately would build the mechanism twice.

   **Decide two open rows before writing a line**, because both change where code goes: whether a
   non-empty *agent floor* under `none` is an error (it must not be `Compose`'s call — see above),
   and whether an allowlist under **`open`** is an error. The second is a **third instance of the
   same class**: `Compose`'s `default` branch discards `userAllow` for the empty mode exactly as the
   `none` branch does, hidden from the CLI only because `--network-allow` promotes to
   `--network-isolated`, and therefore live on the library and profile doors. It is in neither
   finding.

   **Rule 10 without a new fake.** `prepareSandboxState` runs end to end against the existing
   `fakeRuntime` in `create_prepare_test.go`, untagged and inside `make check` — three cases there
   (`none` + allowlist, `none` + profile ports, `none` + real agent) plus inverting
   `compose_test.go:51` and `create_prepare_test.go:105`, which currently *assert* the silent
   discard. The library door is then covered by a `toInternal` field-carry assertion rather than a
   ~40-method `runtime.Backend` fake in the root package, which is what testing `Client.CreateSandbox`
   directly would cost. `toInternal` is a pure copy, so the two together prove the door.

   **It reaches only new sandboxes.** `restart.go:200` rebuilds state from `netpolicy.json` and
   `meta.Ports` and never re-validates, so an existing `none`-plus-ports sandbox keeps relaunching.
   Say so in the BREAKING-CHANGES entry, and decide whether step 3's migrator repairs or rejects
   such a record — that decision sets step 3's scope.
2. **Correct the `none` claims in shipped help and `netpolicy.md`** ([DF198](../findings-unresolved.md)),
   then fix or refuse `none` per backend. The text has to be rewritten for the new modes anyway, so
   it lands with them rather than ahead of them.
3. **`--network=` enum**, booleans as deprecated aliases, both registered in `deprecations.md` with
   the date incurred (rule 9). Carries the schema bump and the migrator.
4. **`restricted` becomes selectable** once `egress-proxy-build.md` has something behind it. Until
   then the value does not exist, rather than existing and degrading — which D138 retires.

## Surfaces to sweep (rule 2)

The flags and mode strings reach roughly forty live files. **`architecture/code-map.md` is gated**
(D124) and is already stale here — it names `loadIsolatedMeta` / `saveNetworkAllowlist` /
`tryLivePatchNetwork`, none of which exist under those names.

- **Shipped text:** `README.md`, `docs/GUIDE.md` (six sites including `:909`), `docs/ROADMAP.md`,
  `internal/cli/helpcmd/help/{security,flags,topics}.md`, `internal/config/defaults.go` (the
  commented default config), `profile.go` (the profile scaffold).
- **CLI:** `lifecycle/{new,run}.go`, `sandboxcmd/{allowed,allow,deny,info,sandbox}.go`,
  `cli/profile/profile.go` (the `network.isolated` JSON key, human output, diff).
- **Library / orchestrator:** `create/create.go` (the `NetworkMode` typedef and constants),
  `aliases.go`, `types.go`, `sandbox.go`, `sandbox_options.go`, `environment.go`,
  `profile_config.go`, `launch/launch.go` (six mode literals), `status/status.go`,
  `envsetup/context.go`, `netpolicy/{compose,strategy}.go`, `netpolicycfg/`,
  `runtimeconfig/runtimeconfig.go` and its patch path, `config/{config,profile,defaults}.go`,
  `agent/agent.go` (`RealAgents`), `mcpsrv/` — network-blind today: no tool accepts a mode and
  `sandbox_status` drops `network_mode`.
- **Runtime / guest:** `runtime/runtime.go` (`BackendCaps`, the mode doc comment),
  `docker/docker.go`, `seatbelt/profile.go`, `apple/apple.go`, `containerd/lifecycle.go`,
  `isolation.go`, `docker/resources/{entrypoint,firewall,install-firewall}.py`,
  `runtimetest/conformance.go`.
- **Contributor docs:** `architecture/code-map.md` (gated), `design/{commands,config,security,
  environments,overview}.md`, `netpolicy.md`, `network-isolation.md`,
  `principles/{security,general}-principles.md`, `backend-idiosyncrasies.md`, and the live plans
  `enforcement-build.md`, `egress-proxy-build.md`, `ipv6-network-isolation.md`,
  `guest-network-families.md`, `seatbelt-host-pf-enforcement.md`, `macos-pf-privileged-path.md`.
- **Tests / scripts:** `scripts/smoke_test.py`, `test/e2e/json_test.go`, `cli/integration_test.go`,
  and the ~13 test files asserting on the current flags.
