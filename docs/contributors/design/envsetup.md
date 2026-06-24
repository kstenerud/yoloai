# Envsetup — the inside-the-sandbox environment preparer

**Status:** Design converged 2026-06-24 (design conversation), not yet implemented. The **envsetup** refinement
of [plans/public-layering.md](plans/public-layering.md) — the dual of the substrate
([D84](../decisions/working-notes.md)): substrate provisions the agent-free *shell*, envsetup provisions its
agent-specific *contents*. The runner for several agent-layer ([D89](../decisions/working-notes.md))
capabilities, and the security **home** where credentials cross into the sandbox
([DF38](findings-unresolved.md)/[DF39](findings-unresolved.md)).

**One-line definition.** Envsetup stages the sandbox's agent-specific contents — credentials, seed files,
settings patches, the assembled context (`DEF`), and the resolved env — **host-side**, before the neutral
container runs, from `{agent-declared shapes + caller-supplied values + the assembled DEF}`.

## The model (the decisions behind the surface)

1. **The dual of the substrate.** Substrate = the agent-free shell (mounts, network namespace, resources,
   neutral PID 1). Envsetup = the shell's agent-specific *contents*: secrets, the agent's home config (seed
   files, where the user's global config `ABC` lands), settings patches (the agent's self-config), the `DEF`
   context delivery, and the resolved agent env. Both "provision" — at different levels.

2. **Heavily pre-pinned; mostly a gathering.** The re-homing ([D89](../decisions/working-notes.md)) handed
   envsetup its inputs (credential-shape + seed-list + self-config + exclude-rules + the `DEF`-deliverer);
   [D63] fixed the credential what/who (caller supplies *values* via the `Env` snapshot, *zero* ambient reads,
   staging via a `SecretsStagingDir`); the [D88](../decisions/working-notes.md) carve made it **host-side
   staging** into the sandbox's on-disk state *before* the neutral container runs. Mechanically, envsetup
   gathers today's scattered `provision/` logic (`CreateSecretsDir`, `CopySeedFiles`, `EnsureContainerSettings`,
   the `DEF` append, env resolution) into one layer.

3. **An agnostic `EnvSpec` interface (separability).** Envsetup consumes an **agent-produced `EnvSpec`**
   (credential-shape + seed-list + settings-patches + context-method + resolved env), not the agent package
   directly — so envsetup stays agent-agnostic in its interface, exactly as substrate takes `ProvisionSpec` and
   session takes `ProcSpec`. The agent layer *compiles* its declarations into the `EnvSpec`. (Env-var boundary:
   the substrate's `ProvisionSpec.env` is the container's base env; envsetup resolves the *agent* runtime env —
   config + profile + caller overlay — and stages credentials, both from the one `Env` snapshot per D63.)

4. **Envsetup is the security *home* for credential crossing (DF38/DF39).** Credentials physically enter the
   sandbox here, two ways: the **secrets staging** (caller values → the bind-mounted secrets dir) and the
   **seed-file copy** (the agent's host config, which may itself carry credentials, e.g.
   `~/.claude/.credentials.json`). So the two deferred credential findings *live* in envsetup — the other
   layers only pointed at them: **DF38** (secure credential delivery — tool-arg injection is wrong, a
   secure-secrets model is needed) is *how the secrets dir is populated*; **DF39** (the `$HOME` credential-file
   bleed — host creds mounted into an *untrusted* sandbox) is *the seed-file copy*. Acute for the metered-JV-key
   + adversarial-agent direction: envsetup is the exact membrane where a real key crosses into an untrusted box.

5. **Baseline now + the secure-secrets seam (the load-bearing room).** Like netpolicy seamed the egress-proxy,
   envsetup establishes the honest baseline (D63: caller `Env` + `SecretsStagingDir`) and **seams the
   committed-future secure model** without building it now. The structural room:
   - the credential-staging mechanism must **not assume** "stage plaintext files into a bind-mounted dir" is
     the only path — a future secure path (a broker, ephemeral injection, or no-host-creds-at-all) must slot in;
   - the host-config seed must be able to become **opt-in / filtered** rather than wholesale-copy (DF39).

## What envsetup stages

| Stage | From (shape) | From (value) | Notes |
|---|---|---|---|
| **Secrets** | agent: env-var names + seed-file list | caller: the `Env` snapshot + host files (D63) | bind-mounted secrets dir; the DF38 seam |
| **Seed files** | agent: `SeedFiles` + `StateDir` + `AgentFilesExclude` | the agent's host config (incl. `ABC`) | auth-only files skipped if a key is set; the DF39 seam |
| **Settings** | agent: self-config key-flips + the `ApplySettings` residual | — | host-side patch of the seeded `settings.json` |
| **Context (`DEF`)** | agent: the injection *method* (append-at-file / launch-flag) | the assembled `DEF` (fan-in) | append to the seeded `ABC`, don't clobber (D89) |
| **Env** | config + profile env | caller overlay (wins) | the agent runtime env, distinct from `ProvisionSpec.env` |

## Cross-references

- **Decisions:** [D84](../decisions/working-notes.md) (substrate — envsetup is its dual),
  [D88](../decisions/working-notes.md) (the carve — host-side staging off the entrypoint),
  [D89](../decisions/working-notes.md) (the agent layer produces the `EnvSpec`); the credential what/who is
  [D63] (caller `Env`, zero ambient reads, `SecretsStagingDir`); this layer's own entry **D91**.
- **Findings:** envsetup is the home of [DF38](findings-unresolved.md) (secure credential delivery) and
  [DF39](findings-unresolved.md) (the `$HOME` credential bleed) — both seamed here, builds deferred.
- **Consumer:** control-eval — the metered-JV-key + adversarial-agent case is the driver for the secure-secrets
  seam (a real key crossing into an untrusted sandbox is exactly envsetup's membrane).
