# Per-agent custom detection strategies — deferred follow-up (MERGE GATE)

**Status:** Planned, not started. Deferred out of the agent-owned-detection plan
([agent-owned-detection.md](agent-owned-detection.md)) on 2026-06-25. The
abstraction's contract is best shaped with all the real callback shapes in hand,
so it lands as its own task.

> **🚧 MERGE GATE.** The `public-layering` branch **does not merge to `main`**
> until this work is done: every shipped agent that exposes a native
> turn-completion callback uses a custom detection strategy (not the heuristic
> fallback). Decided 2026-06-25.

## Progress

- **Phase A — Gemini ✅ wired (57605f5e)**, hook-authoritative via `BeforeAgent`→active
  / `AfterAgent`→idle in settings.json (existing `ApplySettings` mechanism; commands
  append `printf '{}'` for Gemini's stdout-JSON contract). Config + command verified;
  **live-fire blocked by [DF48](../findings-unresolved.md)** (Gemini interactive
  first-run onboarding).
- **Phase B — Codex ✅ DONE (0d523fb1), FULLY VERIFIED LIVE.** Hook-authoritative
  (full start+stop, like Claude) via a dedicated `~/.codex/hooks.json`: `UserPromptSubmit`
  /`PreToolUse`→active, `Stop`→idle, nested under a top-level `hooks` key. Generalized
  the settings-write (`SettingsPatch.FileName`/`Definition.SettingsFileName`) — no TOML
  patching. Launch with `--dangerously-bypass-hook-trust` (sandbox is the trust
  boundary). Also fixed a latent ApplySettings non-idempotency (shared `appendHookGroup`).
  Codex auths via seeded `auth.json` (no env key), so a real turn ran: hook log shows
  `hook.active`+`hook.idle` written by codex, status active→idle, no blip.
- **Phase C — OpenCode ✅ DONE (542a51d7), VERIFIED LIVE.** Hook-authoritative via a
  yoloai-provided plugin (`opencode_plugin.js`, go:embed'd → seeded to
  `~/.config/opencode/plugins/`): keys off `session.status` (`{status:{type:busy|idle}}`)
  — NOT message.updated/session.idle, which fire *after* turn completion (verified, would
  stick at active). New `SeedFile.Content` mechanism for yoloai-provided (non-host) files.
  OpenCode auths via seeded auth.json; real turn → active→idle. Also fixed a Makefile
  stale-binary bug (embed deps; [[make-build-embed-deps]]).
- **Phase D — Aider ✅ DONE (30fb192a).** Hook-authoritative for idle via
  `--notifications-command` (reuses `--write-status idle`). Stop-only (no turn-start),
  so active comes from prompt-delivery's active-before-submit; a user-typed-via-attach
  turn shows stale-idle until it completes — a known gap a future **hook-assisted** mode
  would close (chosen deliberately over deferring aider). Config-verified only (aider
  create is auth-gated, no aider key): command renders correctly, idle_mode
  hook-authoritative, the command writes idle; live-fire relies on aider's documented
  behavior.

## Status — all four wired (merge-gate wiring requirement met)

Every shipped agent that exposes a native turn-completion callback now uses it
(Claude was already; +Gemini, Codex, OpenCode, Aider). **The `DetectionSpec`
formalization turned out unnecessary** — the existing `Idle.Hook` + `idle_mode`
model plus per-agent *registration mechanisms* (settings.json `ApplySettings`,
codex `hooks.json` via `SettingsFileName`, opencode plugin via `SeedFile.Content`,
aider launch flag) accommodated every strategy without a new abstraction. This
validates deferring it as premature (D96 refinement).

**Residual verification gaps** (wiring done, live-fire not fully confirmed):
- Gemini: config+command verified; live-fire blocked by [DF48](../findings-unresolved.md)
  (gemini interactive onboarding).
- Aider: config verified; no aider auth to drive a real turn; the stop-only
  active-gap awaits a future **hook-assisted** mode.

## What

Promote detection from today's informal two-strategy form (an `idle_mode` enum the
python monitor branches on — `hook-authoritative` for Claude, `heuristic-only` for
the rest) into a **first-class per-agent strategy**, and wire the native callback
each agent actually exposes. The heuristic detector stack becomes the **floor**
(legacy versions, unknown/file-defined agents), not the common case.

The callback shapes are already surveyed and verified in
[research/agent-callbacks.md](../research/agent-callbacks.md):

| Agent | Native turn-completion signal | Family |
|---|---|---|
| Claude | `Stop` hook (already wired) | agent-runs-our-command |
| Codex | `notify` program → `agent-turn-complete` (rich JSON) | agent-runs-our-command |
| Gemini | `AfterAgent` hook (GA ≥ v0.26.0; **not** `Stop`) | agent-runs-our-command |
| OpenCode | `session.idle` (plugin `event` hook **or** SSE `/event`) | we-subscribe-to-a-stream |
| Aider | `--notifications-command` (fires on turn-done, **no payload**) | agent-runs-our-command |

## Why deferred (and why it's its own task)

- **Shape risk.** The strategies split into two structural families —
  *agent-runs-a-command-that-writes-status* (Claude/Codex/Gemini/Aider) and
  *we-subscribe-to-a-stream* (OpenCode SSE). Designing the strategy contract with
  only the first family built risks baking the wrong contract and reshaping it when
  the subscribe family lands. Shape it once, with all three shapes in hand.
- **Thin contract.** Aider's payload-free pulse forces the contract to be a thin
  "a turn ended" signal, not "hand me the assistant message" (DD2).
- Not a blocker for the fall-to-shell + resume feature, which works today across
  both existing strategies (hook + heuristic).

## Scope (when picked up)

- The compiled **`DetectionSpec`** (DD2): strategy identity + params + the callback
  registration shape, compiled at the orchestrator boundary (mirroring
  `envspec.BuildEnvSpec`).
- The **python spine** (agent-layer.md openness refinement): a per-agent namespaced
  python module for the strategy implementation where one is needed (the
  subscribe-family especially); the command-writes-status family mostly reuses the
  existing "agent writes the status file" path with a per-agent registration.
- Per-agent registration: Codex `notify`, Gemini `AfterAgent`, OpenCode
  `session.idle`, Aider `--notifications-command`. Version-gate where needed
  (Gemini ≥ v0.26.0) and fall back to heuristic when the callback is unavailable.
- Honest characterization: an agent on the heuristic floor is *not* claimed to have
  authoritative detection.

## Cross-references

- Reserved seam: [agent-detection.md](../agent-detection.md) DD2 + decision **D96**
  refinement; [agent-layer.md](../agent-layer.md) Openness (the in-sandbox
  python-spine exception).
- Survey: [research/agent-callbacks.md](../research/agent-callbacks.md).
- Sibling, shipped: [agent-owned-detection.md](agent-owned-detection.md)
  (fall-to-shell + resume; this is its deferred strategy-formalization tail).
