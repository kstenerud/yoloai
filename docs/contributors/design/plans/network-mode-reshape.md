> **ABOUTME:** Replace the two network booleans with one four-valued `--network` flag whose values
> name *where the enforcement machinery sits relative to the agent* — and make every mode balk
> loudly at the features it cannot honour, instead of degrading.

# Network mode reshape — one flag, four trust boundaries

- **Status:** PLANNED — designed, no code.
- **Depends on:** egress-proxy-build.md

  Only step 5 needs it — steps 1–4 depend on nothing.
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

**Each value names a trust boundary, not a mechanism**, and that is what keeps this compatible with
[netpolicy.md](../netpolicy.md) § *Hostile containment*, which says mode is the **intent** and
strategy is the **realization**, and warns against adding a mode where a strategy would do.
*In-sandbox* versus *out-of-sandbox* is intent — it is the question *"can the thing I am containing
switch it off?"* — and it is answered before any mechanism is chosen. `ip-filter` and `egress-proxy`
remain strategies underneath. **That carve stands; this plan does not overturn it.**

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

**Where the checks belong: below the CLI.** Every refusal has at least three doors — the flag, a
profile, and the library API — and `netpolicy.Compose` (single call site, `prepare_dirs.go:342`) is
downstream of all of them. The existing `--port`-under-`none` check is the counter-example to copy
*away* from: `new.go:207` reads the flag only, so profile- and archetype-supplied ports slip past
([DF197](../findings-unresolved.md)). The runtime path already gets this right — `Network.Allow`
refuses on a `none` sandbox — and creation is the one entry point that does not.

**One correction to an earlier draft, because it changes what the argument rests on:** a profile has
no mode key at all. `config.NetworkConfig` is `Isolated bool` + `Allow []string`, so a profile can
supply the allowlist half but never `none`. The conclusion survives — all doors still converge on
`Compose` — but four modes mean a **new config key**, which is a rule-1 rename carrying a 12-month
user-facing deprecation, a real parser (`handleYoloaiNetwork` validates nothing today), and edits to
the shipped default config and the profile scaffold.

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

1. **`Compose` refuses what a mode cannot honour** — allowlist under `none`, real agent under
   `none`. Fixes a live defect ([DF196](../findings-unresolved.md)); needs nothing else.
2. **Move `--port`'s refusal below the CLI** ([DF197](../findings-unresolved.md)) — same shape, same
   layer, second instance of the class, so fix them together (GEN §18).
3. **Correct the `none` claims in shipped help and `netpolicy.md`** ([DF198](../findings-unresolved.md)),
   then fix or refuse `none` per backend.
4. **`--network=` enum**, booleans as deprecated aliases, both registered in `deprecations.md` with
   the date incurred (rule 9). Carries the schema bump and the migrator.
5. **`restricted` becomes selectable** once `egress-proxy-build.md` has something behind it. Until
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
