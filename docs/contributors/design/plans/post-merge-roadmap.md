> **ABOUTME:** Roadmap of the work remaining after the public-layering merge (D99) — sized and
> sequenced by read-only survey against the live designs, tracking what's blocked, retired, or
> ready to start. Living document: check status inline before assuming any workstream is still
> open.

# Post-merge roadmap (after the public-layering merge, `b9c91834`)

- **Status:** IN-PROGRESS — sequences the D99 post-merge remainder rather than building any of
  it; every workstream has its own plan, and that plan's Status line is authoritative for where
  it stands. The per-workstream status in the table below is a copy of those and is known to lag
  (DF103). E1, the Linux/KVM microvm backend, was investigated and retired (D104).
- **Depends on:** —

The public-layering endgame (D99) is merged. This is the remaining work: the D99
post-merge remainder and the open findings. (E1, a Linux/KVM **microvm** backend,
was investigated and **retired** — see [D104](../../decisions/working-notes.md#d104--retire-the-hand-rolled-qemu--m-microvm-backend-libkrun-is-the-tech-if-a-light-vm-tier-is-ever-added-e1)
and the [archived plan](../../archive/plans/microvm-backend.md).) Scoped 2026-06-27
by four read-only survey agents against the live designs; sizes are S/M/L/XL.

None of this needs a second on-disk migration (the netpolicy relocation, D103,
made `system migrate` the last one).

## Quick-reference table

| # | Workstream | Size | Platform | Blocked by | Unblocks |
|---|---|---|---|---|---|
| A1 | Promote `netpolicy` → public | S | any | — (DAG already clean) | layer story |
| A2 | Promote `envsetup` → public | S | any | — (DAG already clean) | layer story |
| A3 | DF54 — E2E smoke for `run`/`diff --json`/`sandbox_run` | S | Linux+Mac | — | confidence |
| A4 | DF49 — no-workdir `run` mode | M | any | — | run ergonomics |
| A5 | Op: concurrency guard (per-sandbox flock) | S | any | — | daemon/CI use |
| A6 | Op: log rotation | S | any | — | — |
| A7 | macOS DF40 (Tart ASCII) + DF53 (tart `-p` ports) | XS+S | **macOS only** | — | tart polish |
| B1 | Session-carve 1a-iii — `IOSession` on `sb.Agent()`, retire `runtime.InteractiveSession` | L | any | — (ready now) | B2, C, DF50, session promotion, tier-2 |
| B2 | Session-carve 1a-iv — slim `sandbox-setup.py`, extend `AgentFreeLaunch` beyond docker, retire legacy weld | M | any | B1 | DF50, non-docker run |
| B3 | Session-carve 1a-v — three-bucket schema naming | S | any | B1 | — |
| B4 | Promote `session` → public | S | any | B1 (package must exist) | layer story |
| B5 | Stream `SessionKind` (no-tmux channel) | L | any | B1/B2 + **a consumer** | eval-at-scale |
| C | `Sandbox.Usage()` (cost/token ledger) | M–L | any | benefits from B1/B2 | observability |
| D | **Egress proxy** (D95 broker + D90 hostile containment — ONE proxy) | XL | per-backend | spike + decisions | secure-secrets, hostile net |
| ~~E1~~ | ~~**microvm** backend (QEMU `-M microvm`)~~ — **RETIRED (D104)**; libkrun if ever revived | — | — | — | — |
| E2 | apple-container backend (research done, impl-ready) | M–L | **macOS** | naming decision | mac container isolation |
| E3 | podman + gVisor (research-gated) | M (investig.) | Linux/mac | R1 spike | rootless gVisor |

## Workstream detail

### A — Quick wins / finish the layering (independent, low-risk)
- **A1 `netpolicy` / A2 `envsetup` promotion:** both DAGs are **already clean** — the "netpolicy → `internal/agent` upward dep" the old notes flag is stale (the import lives in a *caller*, `orchestrator/create/prepare_dirs.go`, not in netpolicy; the floor is passed as `[]string`). Promotion is near-pure `git mv` + import sweep + depguard fence + a D-entry + surface audit, exactly like the runtime/store/copyflow Move. After these, 5 of 6 layers are public (session is the holdout, pending B).
- **A3 DF54:** add `run` (success/failure exit codes, `--rm`), `diff --json`, and `sandbox_run` cases to `scripts/smoke_test.py` (real Docker + test agent). Optionally extract a thin interface so `executeRun`/`waitForRunResult` become unit-stubbable.
- **A4 DF49:** break the `workdir = Dirs[0]` invariant across ~25 readers; add a conditional no-workdir provisioning path + `ChangeState=not-applicable`. Removes the interim "run requires a workdir" guard.
- **A5 concurrency guard / A6 log rotation:** per-sandbox `flock` at mutating-op entry (becomes a hard dep for any daemon/CI use); size-capped rotation for `log.txt`. Both small; `plans/README.md` describes them.
- **A7 (macOS batch):** DF40 = pass `tmux -u` in Tart's `AttachCommand` (one-liner, diagnosed); DF53 = wire `BuildNetworkArgs` into `buildRunArgs`, flip `NetworkIsolation:true`, verify `--net-softnet`. **Can only be done/verified on macOS.**

### B — Session layer (the long pole; highest leverage)
The carve's structural core (Launch/keepalive/`ProcessLauncher`, D88 S0–S3) is **built**. 1a-i (run/headless) and 1a-ii (AgentLaunchPrefix off descriptor) are **done**.
- **B1 (1a-iii, L):** gather the ~15–20 tmux call sites (`restart`, `attach`, `terminal`, `bugreport`, cli, mcpsrv) behind an `IOSession` handle on `sb.Agent()`; move `TmuxSocket`/`AttachCommand` off the public `runtime.InteractiveSession` (the interface leaves the substrate surface); fold in the tier-2 "write `active` before delivery" race fix. **Ready to start now.**
- **B2 (1a-iv, M):** slim `sandbox-setup.py` to a session-runner; decide per non-docker backend whether to extend `AgentFreeLaunch` or keep the legacy weld; retire/confirm the legacy path. Unblocks **DF50** (headless no-TTY).
- **B3 (1a-v, S):** name the three-bucket schema (`ProvisionSpec`/`AgentLaunchSpec`/session) — structs exist, naming only.
- **B4:** once B1 crystallizes a `session` package, promote it public (small `git mv`).
- **B5 Stream SessionKind (L):** deferred by the real-demand rule — **needs a named consumer** (e.g. eval-at-scale). Leave the seam.

### C — `Sandbox.Usage()` (M claude-first / L all-agents)
Three coupled parts: (1) **stdout capture** — redirect the headless agent's stdout to a log file instead of the uncaptured tmux pane, with a `store` path to read it (slots cleanly into the carved `ProcSpec`, hence the B-benefit); (2) **`--output-format stream-json` per agent** — claude emits `total_cost_usd`/`usage`; gemini/codex/aider don't (no unified schema → claude-first is M, all-agents is L); (3) the **opt-in-vs-always-on fork** (the format change suppresses the TUI a concurrent `attach` would show).

### D — Egress proxy (XL; security-critical; D95 + D90 are ONE proxy)
**The four "before D" decisions + the TLS-pinning spike are now settled — see [D105](../../decisions/working-notes.md#d105--egress-proxy-workstream-d-brokering-is-the-default-containment-is-opt-in-phased-by-credential-material-refines-d90d95).** Summary of the settled shape (refines the original framing below):
- **`base_url` redirect, not transparent MITM** (no agent pins certs; all SDKs honour `base_url`). MITM is a deferred fallback only.
- **Brokering the agent's API key is the always-on default; egress restriction stays opt-in** ("API keys brokered; egress open unless restricted").
- **Two layers:** an always-on tiny fixed-upstream **key-injector** (general traffic direct) + an opt-in **default-deny netns + SNI-splicing forwarder + allowlist** (subsumes the injector when on).
- **Proxy tech = bespoke small Go** (no Python/SaaS deps). **Enforcement = Linux-first** (host nftables on the veth; uniquely fixes gVisor). **macOS deferred.**
- **Phase by credential material:** (1) metered API keys, (2) subscription OAuth (proxy owns the host-side refresh token + refresh loop), (later) Bedrock/Vertex + git broker. **Direct delivery retained as a per-backend transitional fallback (no flag-day).** The broker stays **general** (git + other auth'd tools, not LLM-only).

Refined build order: `CredentialSource` + general `EnvSpec` credential-shape → always-on key-injector (metered) → subscription-OAuth broker → opt-in egress containment (nftables + SNI forwarder + allowlist; strategy-dispatch `LivePatchNetwork`) → (later) MITM fallback, macOS enforcement, git broker. Reserved seams to fill: `StrategyEgressProxy`, `netpolicycfg` record, `Compose`, `EnvSpec` fields, `LivePatchNetwork`.

_Original framing (superseded by D105): a single TLS-MITM L7 proxy; build CredentialSource → MITM process → default-deny netns → per-agent CA injection → strategy dispatch._

### E — New backends
- **~~E1 microvm~~ — RETIRED (D104, 2026-06-28).** The QEMU `-M microvm` path was built and spiked, then retired: it can't boot a stock distro kernel (custom-kernel-only after the `6.12.94` bump), and a lighter microVM adds no isolation over the existing Kata `vm` backend and no boot benefit for long sessions. If a light VM tier is ever revived it's **libkrun** (bundled Red-Hat kernel via `libkrunfw`, virtio-fs, OCI-native, also macOS HVF), not QEMU-microvm — gated on Debian packaging + a macOS virtio-fs perm fix. See [D104](../../decisions/working-notes.md#d104--retire-the-hand-rolled-qemu--m-microvm-backend-libkrun-is-the-tech-if-a-light-vm-tier-is-ever-added-e1) and the [archived plan](../../archive/plans/microvm-backend.md). Spike preserved on the unmerged `microvm-backend` branch.
- **E2 apple-container (M–L, macOS):** all research resolved positively (virtiofs mounts, in-guest overlayfs, in-guest iptables, `--format json`, exit codes) — **implementation-ready**; one live confirmation (vmnet gateway in the isolation OUTPUT chain). Naming (`apple` vs `apple-container`) decided first.
- **E3 podman + gVisor (M investigative):** R1 (does rootless podman + gVisor actually work, and how?) gates the design; R2 (macOS Podman Machine runsc) and R3 (compat-API `Runtime=runsc`) run in parallel once a runsc env exists. The codebase currently has an evidence-free blanket block to validate-or-lift.

## Decisions a human must make before building

**Session layer (before B1/B2):**
- Q1: Does `runtime.InteractiveSession.TmuxSocket` move into the session layer as a backend-query, or does the session layer compute the socket path from locality? (Determines whether the interface disappears or relocates.)
- Q2: Per non-docker backend (tart/seatbelt/podman/containerd/apple) — extend `AgentFreeLaunch`, or confirm the legacy weld is the acceptable shape? (Seatbelt host-process-group + Tart VM-guest launch each need an explicit answer.)
- Q3: Is tier-2 hook-authoritative idle in scope for B1, or a follow-on?

**Usage (before C):** opt-in (`run --capture-usage`) vs always-on; claude-first vs all-agents.

**~~Egress proxy (before D)~~ — SETTLED in [D105](../../decisions/working-notes.md#d105--egress-proxy-workstream-d-brokering-is-the-default-containment-is-opt-in-phased-by-credential-material-refines-d90d95).** Proxy tech → bespoke small Go. Default-deny → host-netns nftables on the veth, Linux-first. Trust-posture → brokering the agent's API key is always-on by default (no flag needed); egress restriction stays opt-in. Locality → per-sandbox. TLS-pinning spike → no agent pins; `base_url` redirect chosen over MITM.

**~~microvm (before E1)~~ — moot (E1 retired, D104).** The kernel-strategy decision was the crux: the distro-kernel choice proved unviable on `-M microvm` (custom-kernel-only), which is what retired the backend. If libkrun is ever revived these decisions are reframed entirely (it brings its own kernel + virtio-fs).

**apple-container (before E2):** `apple` vs `apple-container` backend name.

## Recommended sequence

1. **Phase 0 — finish the layering + quick wins (parallel, decision-free):** A1+A2 (netpolicy/envsetup promotions → 5/6 layers public), A3 (DF54 smoke), A5 (concurrency guard), and the macOS A7 batch when a Mac run is available. Fast value, low risk, builds momentum.
2. **Phase 1 — session-carve (B1 → B2 → B3 → B4):** the long pole. Settle Q1–Q3 first, then it unblocks DF50, the session promotion, Usage's clean stdout path, and tier-2. Highest downstream leverage.
3. **Phase 2 — `Usage()` (C):** rides the carved headless path; decide the two forks.
4. **Phase 3 — egress proxy (D):** open with the TLS-pinning spike + the design decisions, then build the unified proxy. Biggest + most security-critical.
5. **Backends track (interleave per appetite/hardware):** ~~microvm (E1)~~ **retired (D104)**; **apple-container (E2)** is impl-ready but macOS; **podman+gVisor (E3)** is a research spike.

Pick any workstream to start — Phase 0 has no upstream blockers and the least "decide first" overhead.
