# Egress proxy (workstream D) — build brief

ABOUTME: The "start here" kickoff for building the egress-proxy credential-broker. Design is
settled and validated; this is the actionable build plan. Rationale lives in D105 (+ its
validation addendum) in decisions/working-notes.md; do not re-litigate it — build to it.

**Status:** **Phases 1 & 2 COMPLETE + launch-path decoupling COMPLETE, all on `main`,
end-to-end verified on real Docker (2026-06-28).** Brokering is the default for Claude on docker —
both the metered API key (`ANTHROPIC_API_KEY`, x-api-key) and the subscription token
(`CLAUDE_CODE_OAUTH_TOKEN`, Authorization: Bearer); opt out with `--no-broker`. Brokering is now
**decoupled from the launch path** (works on agent-free *and* legacy bring-up). **All three Linux
backends broker — docker, containerd/Kata, and rootless podman — verified on real hardware.** The
**macOS backends are done too** (2026-06-28): docker (OrbStack/Desktop), seatbelt, and apple broker;
tart degrades to direct delivery (per-VM ephemeral bridge, not bindable pre-create). Next is egress
containment (build step 4). Read first:
[D105 + validation addendum](../../decisions/working-notes.md#d105--egress-proxy-workstream-d-brokering-is-the-default-containment-is-opt-in-phased-by-credential-material-refines-d90d95)
and [D106](../../decisions/working-notes.md#d106--egress-proxy-the-key-injectors-lifetime-is-a-pluggable-host-cli-sidecar--embedder-in-process-recovery-is-lazy-re-derivation-refines-d105),
then [secure-secrets.md](../secure-secrets.md) (D95) and [netpolicy.md](../netpolicy.md) (D90),
then the spike at [research/egress-broker-spike/](../research/egress-broker-spike/). **Resuming?
Read the Build progress section below first.**

## Build progress (2026-06-28)

Phases 1 (metered API keys) and 2 (subscription OAuth token) are **done**. Phase 1 is merged
to `main` (not pushed); Phase 2 lives on branch `broker-subscription-oauth` (not yet merged).
What landed, in order — each a `--no-ff` merge:

- **`internal/credential`** — the tool-agnostic `CredentialBinding{Destination, Apply, Source}`.
  Full closed shape reserved (D105 addendum); `header-set`/`basic-auth` + `static`/`refreshing`
  implemented, `request-signer`/`minting` are `ErrNotImplemented` stubs.
- **`internal/broker`** — `Injector` (fixed-upstream reverse proxy, strip placeholder + inject
  real key, stream SSE); `RunSidecar` (the out-of-process injector body, secret via stdin, addr
  via stdout handshake); `SidecarHost` (the CLI `InjectorHost`: spawn `os.Executable() __inject`
  Setsid-detached, persist `injector.json` = PID+addr no-secret, `Ensure`/reconcile, reap).
  Stable-port respawn so a reconciled injector stays reachable.
- **`runtime.InjectorReachable`** seam → docker impl reads the bridge gateway (`InjectorReach{
  BindHost,DialHost}`; the split exists for Docker Desktop). podman inherits it.
- **`__inject` entrypoint dispatch** in `cmd/yoloai/main.go` (before cobra; empty-env safe).
- **Launch wiring** (`startViaLaunch` → `brokerCredentials`): after `waitForReady`, start the
  injector and rewrite `secretEnv` (drop real key; set base_url=gateway:port + dummy token).
  `applyBrokerEnv` is the pure, tested env-swap. `lifecycle` Stop/Destroy reap; reconcile fires
  on single-sandbox commands (attach/info/diff).
- **Default flip + `--no-broker`** (D105(b)): brokering is auto-on for a brokerable agent on a
  supporting backend with a key + open networking; tri-state via two sticky-persisted meta
  bools (`BrokerCredentials` forced-on / `BrokerDisabled` forced-off). **Guard:** auto-skips
  under `--network-isolated`/`--network-none` (the in-sandbox allowlist can't reach the gateway
  injector yet); explicit `--broker` there errors. Documented in BREAKING-CHANGES.md + GUIDE.md.
- **Integration test** `TestIntegration_CredentialBroker` (`//go:build integration`, real
  Docker): real key never enters the container, env points at the injector, container→gateway→
  injector→mock swaps the key host-side, reaped on destroy. The integration `TestMain` dispatches
  `__inject` so the test binary can serve as the injector.

Key files: `internal/credential/`, `internal/broker/`, `runtime/docker/reach.go`,
`internal/orchestrator/launch/launch.go` (`brokerCredentials`/`applyBrokerEnv`/`ReconcileInjector`/
`buildInjectorSpec`), `internal/orchestrator/lifecycle/{start,restart,lifecycle}.go`,
`internal/agent/agent.go` (`BrokerConfig` on Claude only), `store/environment.go`.

- **Phase 2 — subscription OAuth (build step 3) — LANDED on branch `broker-subscription-oauth`.**
  `BrokerConfig` generalized from one API key to a precedence-ordered `Credentials []BrokerCredential`
  list; `SelectCredential` picks the first present. Claude declares two: `ANTHROPIC_API_KEY`
  (x-api-key) and `CLAUDE_CODE_OAUTH_TOKEN` (Authorization: Bearer, from `claude setup-token`), API
  key first. The 1-year token stays host-side instead of being delivered into the box. The
  `.credentials.json` refresh-token seed is already suppressed when the OAuth token is present (it is
  in `APIKeyEnvVars` → `HasAnyAPIKey` true), so brokering the token removes the *last* credential —
  no refresh-token leak. `applyBrokerEnv` now drops *every* brokerable credential env var (not just
  the selected one) so an unselected sibling can't leak. The real-Docker integration test runs both
  credential forms end-to-end. We did NOT reverse-engineer Anthropic's OAuth refresh (the
  `~/.claude/.credentials.json` interactive-login path stays on direct delivery).

- **Launch-path decoupling — LANDED on `main`.** Brokering used to fire only on the agent-free
  launch path (`startViaLaunch`), so only docker brokered; every other backend was on the legacy
  path and never brokered. Now `brokerCredentials` runs in `LaunchContainer` *before* the
  agent-free/legacy delivery split, operating on the shared resolved secret map: it starts the
  injector at a **network-level** `InjectorReach()` (the bridge gateway, knowable pre-create — no
  more inspecting a running container) and rewrites the map, which each path delivers its own way
  (`ProcSpec.Env` for agent-free, `/run/secrets` files via the new `StageSecretEnv` for legacy).
  Any backend can now broker, gated only by reachability — not by whether it has a `ProcessLauncher`.
  Verified end-to-end on real Docker across both credential forms AND both launch paths
  (`TestIntegration_CredentialBroker` + `_LegacyPath`). `runtime.ErrInjectorUnsupported` lets a
  reachable-but-not-ready backend (rootless podman) cleanly opt out (auto → direct delivery). This
  makes the parked podman `AgentFreeLaunch` flip moot — podman just needs its slirp `InjectorReach`.

- **All three Linux backends broker — LANDED + verified on real hardware.** **docker** (agent-free
  + legacy), **containerd/Kata** (CNI gateway `10.89.0.1`, gateway-for-both; guest reach verified on
  real Kata), and **rootless podman** (`slirp4netns:allow_host_loopback` → injector on `127.0.0.1`,
  sandbox dials `10.0.2.2`; `RequiredNetworkMode` carried on the reach, applied conditionally only
  when brokering engages). Each has an `InjectorReach`; podman/containerd each got a unit test + a
  real-backend broker integration test (`TestIntegration_CredentialBroker_Podman`, the docker
  `_LegacyPath`). One known edge: the **first** containerd sandbox on a fresh host direct-delivers
  until `yoloai0` exists (safe degrade via `ErrInjectorUnsupported`), then brokers — eager
  bridge-ensure is the follow-up.

**Next up (in order): egress containment (build step 4).** The macOS backends are done (below).

- **macOS variants — LANDED + verified on real hardware (2026-06-28, macOS 26.5 / Apple Silicon;
  OrbStack + tart 2.32 + apple `container` 1.0 + seatbelt all present).** Three of the four backends
  broker; tart degrades. Each is a network-level `InjectorReach`; the launch wiring was already
  backend-agnostic. All mapped in
  [research/egress-broker-host-reachability.md](../research/egress-broker-host-reachability.md).
  - **docker** (`runtime/docker/reach.go`): now **provider-aware**. `isDesktopClassEngine()` (true on
    darwin, or when a Desktop-class provider socket is detected via `providerNames`) returns
    `{BindHost: "127.0.0.1", DialHost: "host.docker.internal"}`; native Linux Engine keeps
    gateway-for-both. Verified end-to-end on OrbStack: the existing
    `TestIntegration_CredentialBroker` runs the full brokered launch on macOS — injector binds
    `127.0.0.1`, the container reaches it via `host.docker.internal`, real credential swapped
    host-side (both API-key and OAuth forms PASS). Live spike confirmed the bridge gateway
    `172.17.0.1` is unreachable from the container (the caveat) and the alias resolves implicitly.
  - **podman** (`runtime/podman/reach.go`): **does not broker on macOS** — returns
    `ErrInjectorUnsupported` on darwin → direct delivery (like tart). podman runs in a podman-machine
    VM; the slirp reach (`10.0.2.2`) reaches the machine VM not the Mac, and the gvproxy host-forward
    (`192.168.127.254`), while curl-reachable, stalls the real agent's streaming traffic (the agent hung
    in the real-agent smoke, where docker/apple brokering passed). Conservative posture restores the
    working pre-broker behavior. Linux rootless keeps slirp; rootful stays unsupported. Guarded by
    `TestIntegration_Podman_DirectDeliveryOnMacOS`; the broker test is darwin-skipped. See DF57.
  - **seatbelt** (`runtime/seatbelt/reach.go`): `{127.0.0.1, 127.0.0.1}` — host process, no netns.
  - **apple** (`runtime/apple/reach.go`): gateway-for-both on the **shared, persistent** vmnet gateway
    (`container network inspect default` → `ipv4Gateway`, e.g. `192.168.64.1`), guarded by
    `ipAssignedToHost` → `ErrInjectorUnsupported` when the bridge is down. The shared bridge is
    host-bindable **before** any VM boots, so it brokers via the pre-create bind like containerd.
  - **tart** (`runtime/tart/reach.go`): returns `ErrInjectorUnsupported` (safe degrade to direct
    delivery). tart runs each VM on a **per-VM, ephemeral** vmnet bridge that exists only while the VM
    runs; the broker binds pre-create (before `Create`/`Start`), so the gateway is assigned to no host
    interface at bind time. tart brokers once the eager-network-prepare follow-up lands (below).
  - Verification: docker via the OrbStack orchestrator broker test; seatbelt/apple via real-backend
    `InjectorReach` bind-checks (`runtime/{seatbelt,apple}/reach_integration_test.go`, mirroring the
    broker's own `SidecarHost.Ensure` → `net.Listen`); plus the manual host-listener + guest-curl
    spikes recorded in the research doc. The full orchestrator-level broker test does **not** fit
    seatbelt/apple cheaply (seatbelt's host-process agent launch needs `node` + a short tmux socket
    path; apple needs the full gated base image) — and the brokering wiring it exercises is
    backend-agnostic and already proven on OrbStack/podman/containerd.
- **containerd eager bridge-ensure** (Linux follow-up): create `yoloai0` before brokering so the
  *first* containerd sandbox brokers too. Generalizes the "prepare network before broker" idea.
- **Egress containment — step 1 (broker × ip-filter composition) DONE, verified on real Docker.**
  Retired the "skip/error under isolation" guard: under `--network-isolated` the broker still
  engages, `brokerCredentials` returns a `brokerOutcome{NetworkMode, InjectorEndpoint}`, and the
  endpoint (`DialHost:port`) is published as `YOLOAI_BROKER_INJECTOR_ENDPOINT`; `entrypoint.py`
  `isolate_network` adds a port-specific iptables ACCEPT for it before the default REJECT. The
  injector port being post-launch is moot — the decoupling already starts the injector pre-Create,
  so the endpoint is known when the container config is built. So a brokered+isolated sandbox keeps
  its credential host-side AND is egress-contained (the agent's LLM egress collapses to the injector;
  a non-allowlisted destination is REJECTed — `TestIntegration_CredentialBroker_Isolated`). Caveat:
  a backend needing a dedicated network mode to reach the injector (rootless podman → slirp) can't
  compose with the isolation bridge+iptables yet → that combo falls back to direct delivery.
  - **Step 1.5 (tamper-resistant firewall) — PLANNED, next build:** step 1's in-container iptables is
    flushable by the agent (`sudo iptables -F`; confirmed — the container holds `NET_ADMIN` and the
    agent has NOPASSWD sudo). Fix (validated on real Docker): deny the agent container `NET_ADMIN` and
    install the firewall from an ephemeral sidecar sharing its netns (`--network container:<id>
    --cap-add NET_ADMIN`); the agent's `sudo iptables -F` then fails (`FLUSH_DENIED`). docker/podman +
    agent-free path first. Full plan:
    [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md).
  - **Step 2 (the hostile-grade future) — NOT built:** `StrategyEgressProxy` = default-deny netns
    whose sole egress is a host-side SNI-splicing forwarder on a separate namespace the agent can't
    disable (survives a sudo agent). Step 1's ip-filter is best-effort (a `NET_ADMIN` agent can
    flush it); step 2 is enforcement-outside-the-agent's-reach. Large, Linux-first; needs the
    strategy-dispatch seam for `Network.Allow/Deny`. See netpolicy.md "Hostile containment".

Per-agent base_url/dummy/force-API-key/telemetry-suppression quirks for Codex/Gemini/Aider/
OpenCode (the table below) live in **launch config** (agent definitions), not the proxy — add a
`BrokerConfig` per agent + its launch knobs when extending beyond Claude.

## The shape in one paragraph

Default posture = **"API keys brokered; egress open unless restricted."** Two layers:
(1) an **always-on key-injector** — a small per-sandbox fixed-upstream reverse proxy; the
agent is pointed at it via `base_url` with a **placeholder** credential, and the proxy swaps
in the real upstream key (general traffic goes direct). (2) an **opt-in egress-containment**
layer — default-deny netns + an SNI-splicing forwarder + allowlist — that subsumes the
injector when enabled. Transport is **`base_url` redirect, not transparent MITM** (proven:
no agent pins certs; all accept plain-`http://` localhost). Bespoke small Go (no
Python/SaaS deps). Linux-first enforcement (host nftables on the veth).

## Build order

1. **`CredentialSource` + general credential-shape (tool-agnostic).** Fill the reserved
   seams (`EnvSpec`, `netpolicycfg`). Reserve the *full* shape now — retrofitting it later
   is a breaking change:
   ```
   CredentialBinding { Destination, Apply, Source }
     Apply  = header-set | basic-auth | request-signer   // closed 3-variant enum
     Source = static | refreshing(Fetch->value,expiresAt) | minting
   ```
   Implement `header-set` + `basic-auth` + `static`/`refreshing` now; **reserve**
   `request-signer` (AWS SigV4 / Azure SharedKey — runs LAST, sees the whole request) and
   `minting` (GitHub-App JWT->1h token; Docker/OCI). Do NOT solve Docker/OCI or SigV4 in the
   proxy yet — route Docker to a `minting` source + an in-sandbox credential helper.
2. **Always-on key-injector — metered API keys (the default).** Per-sandbox forward-and-swap
   reverse proxy: terminate the agent's request, replace the placeholder auth header with the
   real `x-api-key`/`Authorization`, stream the response (SSE) back, forward to the agent's
   configured real upstream. Wire the per-agent launch config (next section). `--no-broker`
   escape hatch.
3. **Subscription-OAuth broker (Phase 2).** Proxy holds the credential host-side and injects
   `Authorization: Bearer`; the agent gets `base_url` + a dummy handle and never holds creds.
   **Prefer the sanctioned `claude setup-token` -> 1-year `CLAUDE_CODE_OAUTH_TOKEN`** held
   host-side over reverse-engineering Anthropic's OAuth refresh (undocumented, rotated,
   discouraged — fragile).
4. **Opt-in egress containment.** Default-deny netns + SNI-splicing TCP forwarder (peek
   plaintext SNI, allow/deny, splice — no decryption) + allowlist. Linux first
   (docker/podman/Kata/gVisor via host nftables on the veth — uniquely gives gVisor a real
   allowlist). Strategy-dispatch `LivePatchNetwork` (`ip-filter` vs `egress-proxy`). macOS
   deferred (Tart/Apple = host `pf` on the TAP; Seatbelt has no netns — hardest).
5. **(later)** MITM fallback for non-redirectable injection; macOS enforcement; Bedrock/Vertex
   (SigV4 signer); the git broker (GitHub-App minting).

**Keep direct delivery (today's `/run/secrets` file/env path) as the per-backend transitional
fallback — no flag-day.** Brokering rolls out backend-by-backend; subscription stays on direct
delivery until Phase 2.

## Per-agent launch config (all brokerable via base_url + dummy cred)

| Agent | base_url + dummy-cred knobs | Must-do |
|---|---|---|
| Claude | `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN=dummy` | pre-seed `~/.claude.json` `hasCompletedOnboarding:true`; allowlist `api.anthropic.com` for WebFetch preflight (or `skipWebFetchPreflight:true`); `ENABLE_TOOL_SEARCH=true` if MCP tool search needed |
| Codex | custom `[model_providers.X] base_url`+`env_key`+`wire_api="responses"`+`forced_login_method="api"` | proxy speaks **Responses API** `/responses`; not the built-in `openai` provider; disable telemetry/update |
| Gemini | `GOOGLE_GEMINI_BASE_URL` + `GEMINI_API_KEY=dummy` | **must** force API-key mode (base_url ignored under OAuth) |
| Aider | `--openai-api-base` + `--openai-api-key dummy` | model name **must** be `openai/<model>`; `--no-check-update --analytics-disable` |
| OpenCode | provider `options.baseURL` + `options.apiKey` | **pre-cache the `@ai-sdk/openai-compatible` npm pkg + models.dev in the image** (runtime fetch precedes the proxy); `autoupdate:false`, `share:"disabled"` |

The injector itself stays simple (forward + header-swap to a per-agent-configured upstream);
all per-agent quirks above live in launch config (agent definitions + envsetup), not the proxy.

## Confirm at build (each ~30-min localhost echo)

- Codex: zero-key-validation + exact Responses request shape.
- Gemini: current base_url env-var spelling (it has churned across versions — pin the CLI).
- OpenCode: `options`-forwarding on the pinned version (issue #5674).
- Subscription: `claude setup-token` token works injected; ToS sanity for brokered subscription.

## Reserved seams already in tree (fill these)

`StrategyEgressProxy` const, the `netpolicycfg` record + `Compose`, `EnvSpec` credential
fields, `LivePatchNetwork` (strategy-dispatch). See the seam map in D90/D95 and the
code-level audit referenced from D105.

## Out of scope / do not over-build

Transparent MITM (base_url redirect makes it unnecessary for the LLM path — keep as a later
fallback only). Docker/OCI token-exchange in the proxy (use a credential helper). macOS
enforcement (Phase 5). Concealment of credentials (impossible by design — goal is bounded
blast radius + detection, composed with the copy/diff/apply review gate + a narrow allowlist
+ egress logging).
