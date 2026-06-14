<!-- ABOUTME: Active queue of open design/implementation questions for the yoloAI project. -->
<!-- ABOUTME: Resolved items drain to questions-resolved.md; deferred to questions-deferred.md; abandoned to questions-abandoned.md. -->

# Open Questions

Questions encountered during design and implementation that need resolution. Resolve each before the relevant implementation begins.

## Codex and cleanup

37. **Codex proxy support** — Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified (see [commands.md](../design/commands.md), [Security Research](research/security.md)). Critical for `--network-isolated` mode with Codex. If it ignores proxy env vars, would need iptables-only enforcement.

38. **Codex required network domains** — Only `api.openai.com` is confirmed (see [commands.md](../design/commands.md)). Additional domains (telemetry, model downloads) may be required.

39. **Codex TUI behavior in tmux** — Interactive mode (`codex --yolo` without `exec`) behavior in tmux is unverified ([Agents Research](research/agents.md)).

75. **Codex follow-up limitation undocumented** — *Reopened (deferral trigger fired).* Was deferred until Codex shipped; Codex is now a first-class agent, so the session-persistence / follow-up limitation needs documenting in the user docs. Original deferral note: "Codex is post-MVP. Document the session persistence limitation when Codex is implemented."

## Workflow Commands

77. **No `yoloai wait` CLI command for scripting/CI** — *Reopened 2026-05-31; library half landed 2026-06-07, CLI half still open.* The **library substrate now exists**: `Sandbox.Wait(ctx, SandboxWaitOptions{For, Timeout})` blocks until the agent reaches the chosen condition (`WaitForExit` / `WaitForIdle`) and returns `ErrWaitTimeout` (wrapping `context.DeadlineExceeded`) on timeout — see `sandbox.go:178-205`. What remains unimplemented is the **CLI surface**: there is no `wait` entry in the command registry. Intended CLI behavior: `yoloai wait <name> [--timeout]` blocks until the named sandbox's agent exits, returns its exit code (124 on `--timeout`, matching `timeout(1)`); thin wrapper over `Sandbox.Wait`. Useful for CI/CD and as the substrate for the deferred `yoloai run` (#56). Design: [plans/README.md `### yoloai wait`](plans/README.md). Prior design refs: [layering.md §9.2](../archive/design/layering.md#92-yoloai-wait-q77), [D17](../archive/design/layering.md#7-decisions).

## macOS Sandbox Backend

94. **macOS VM backend for native development** — yoloAI's Linux Docker containers cannot run xcodebuild, Swift, or Xcode SDKs. Supporting macOS-native development requires a VM-based sandbox backend. Tart (Cirrus Labs) is the leading candidate (see [Sandboxing Research](research/sandboxing.md) "macOS VM Sandbox Research"). **Partially resolved:** The `runtime.Backend` interface in `internal/runtime/` provides the backend abstraction, with Docker, Tart, and Seatbelt implementations. Remaining open questions:
    - ~~**Architecture:** How does yoloAI abstract over Docker (Linux) and Tart (macOS) backends? Shared interface with per-backend implementations? Or separate command paths?~~ **Resolved:** `runtime.Backend` interface with per-backend packages (`internal/runtime/docker/`, `internal/runtime/tart/`, `internal/runtime/seatbelt/`).
    - **Image management:** macOS VM images are ~30-70 GB (vs. ~1 GB for Linux Docker images). How to handle first-run image download? Pre-built images via OCI registry?
    - ~~**2-VM limit:**~~ **Resolved 2026-05-23: detect from Tart, don't hard-code.** Read stderr/`vm.log` for `"The number of VMs exceeds the system limit"`; convert to typed `ErrConcurrentVMLimit`. No hard-coded count — tracks Apple's policy as it evolves. See [D11](../archive/design/layering.md#7-decisions), [`tart-limit-detection.md`](research/tart-limit-detection.md), [W-L14](../archive/plans/layering-refactor.md#w-l14--tart-concurrent-vm-limit-detection-errconcurrentvmlimit). macOS-side verification required before commit.
    - ~~**Xcode installation:**~~ **Resolved 2026-05-23: document as user prerequisite.** Pre-installing inflates download (Xcode is ~30 GB); lazy install needs Apple ID interaction. Revisit if Tart usage shows it's a friction point. See [D12](../archive/design/layering.md#7-decisions).
    - **Agent compatibility:** Do Claude Code and other agents work correctly inside macOS VMs? Any differences from Linux container behavior?
    - **Diff/apply workflow:** Does the copy/diff/apply workflow work unchanged? Tart's VirtioFS sharing may behave differently from Docker bind mounts.
    - **Startup time:** ~5-15 seconds is acceptable but noticeably slower than Docker. Does this affect UX enough to require UI changes (progress indicator)?

## Model Version Tracking

98. **Strategy for keeping model aliases current** — Gemini's model aliases drifted (pointed to 2.5 when Gemini 3 was the current default). This will recur as providers release new models. Need a process to stay current. Options to discuss: periodic manual review cadence, automated checks against provider APIs/docs, pinning to stable identifiers that providers maintain (e.g., `-latest` suffixes where available), or documenting that aliases are best-effort and users should use `--model` for specific versions.

## Public layering (semantic conflations)

Surfaced by the [public-layering](plans/public-layering.md) audit. All four resolved: Q103 + Q106 by [D84](../decisions/working-notes.md), Q104 + Q105 by [D85](../decisions/working-notes.md) → [questions-resolved.md](questions-resolved.md). (Mechanical findings continue in [findings-unresolved.md](findings-unresolved.md): DF31–DF34.)
