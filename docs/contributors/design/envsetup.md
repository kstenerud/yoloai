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
     the only path — a future secure path (a broker, ephemeral injection, or no-host-creds-at-all) must slot in.
     **This includes the in-container *read* side** (D92): today 4+ backend consumers hard-read plaintext files
     at `/run/secrets`; the seam must lift that assumption too, or it's drawn one layer too high;
   - the host-config seed must be able to become **opt-in / filtered** rather than wholesale-copy (DF39).

## What "secure secrets" actually means (threat model)

Concealing an in-use credential from the agent is a **non-goal**: usable implies reachable — an agent with
code-execution and a network path can read or abuse any credential it must use to do its task (an SSH key it
needs for git, an API key it calls). File-vs-env delivery does not change this.

Secure-secrets is instead three things:

1. **Blast-radius / scope.** Deliver a credential whose theft is bounded — a metered/scoped per-task token
   rather than a master key; a repo-scoped deploy key rather than the whole `~/.ssh`. This is largely
   **caller-side**: the embedder supplies a scoped credential via the D63 caller-`Env` snapshot; yoloAI's job
   is faithful delivery and not over-granting.

2. **Bystander-exclusion ([DF39](findings-unresolved.md)).** Do not hand the agent credentials the task does
   not need — filtered/opt-in host-config seed, not a wholesale `~/.claude` copy. This one **yoloAI can
   enforce**.

3. **Exfil-cost reduction.** Broker / just-in-time injection — an egress proxy injecting an API auth header so
   the key never enters the sandbox; ssh-agent forwarding so the agent can sign/push during the session but
   cannot walk away with the key. Not bulletproof, but raises theft from "read a file" to "actively intercept
   your own tool calls" and shrinks the at-rest footprint.

**At-rest hygiene is not a default concern.** yoloAI sandboxes are single-purpose and ephemeral; on a
single-user host the staged secret is the user's own `0600` file (and post-E3, Docker stages no host file at
all). Plaintext-at-rest only matters when **multiple principals' credentials share one host** — an embedded
multi-principal daemon — and that is handled by the per-principal `SecretsStagingDir` knob (an embedder
control), not by default behavior or a runtime warning.

## What envsetup stages

| Stage | From (shape) | From (value) | Notes |
|---|---|---|---|
| **Secrets** | agent: env-var names + seed-file list | caller: the `Env` snapshot + host files (D63) | **Docker/Launch path (E3):** delivered as `ProcSpec.Env` (env-of-the-launched `sandbox-setup.py`), `YOLOAI_SECRET_KEYS` sentinel names which vars are secrets — no host-staged dir. **Legacy backends** (containerd, tart, seatbelt — no `ProcessLauncher`): still a bind-mounted secrets dir at `/run/secrets` (containerd) or `<sandbox>/secrets/` (tart/seatbelt) until they are carved. The DF38 seam. |
| **Seed files** | agent: `SeedFiles` + `StateDir` + `AgentFilesExclude` | the agent's host config (incl. `ABC`) | auth-only files skipped if a key is set; the DF39 seam |
| **Settings** | agent: self-config key-flips + the `ApplySettings` residual | — | host-side patch of the seeded `settings.json` |
| **Context (`DEF`)** | agent: the injection *method* (append-at-file / launch-flag) | the fan-in *fragments* (envsetup **assembles** → `DEF`) | append to the seeded `ABC`, don't clobber (D89) |
| **Env** | config + profile env | caller overlay (wins) | the agent runtime env, distinct from `ProvisionSpec.env` |

## Design-review remediation (2026-06-24, D92)

- **`DEF` has an assembler: envsetup.** The agent declares the injection *method*; the fan-in contributors
  (file-exchange → Q&A, sandbox → orientation, netpolicy → isolation-notice) each supply a *fragment*;
  **envsetup gathers the fragments into the single `DEF`** and delivers it. (Previously the spec said envsetup
  "receives `DEF` pre-assembled" and the agent disclaimed assembly — leaving it ownerless. Resolved: assemble +
  deliver is one job, envsetup's.)
- **Envsetup claims the `entrypoint.py` secrets work** ([DF41](findings-unresolved.md)): for the
  **Docker/Launch path** this is **resolved by dissolution** (E3: credentials are delivered as `ProcSpec.Env`,
  so there is no host-staged `/run/secrets` dir to read and no `.secrets-consumed` marker to write — the
  marker and the read simply do not exist on this path). For **legacy backends** (containerd, tart, seatbelt),
  the secrets read from `/run/secrets` + the `.secrets-consumed` marker handshake (the host↔sandbox signal
  that lets the host delete the staged dir) remain — they re-home to envsetup and become Go-driven steps over
  the neutral keep-alive once those backends are carved. Implemented + verified on real Docker (commit
  163533a9). (UID-remap/overlay → substrate; `isolate_network` → netpolicy.)
- **Ordering is a contract, not an accident.** The stages have a hard happens-before: substrate provision →
  **seed `ABC`** → **append `DEF`** (the Context stage appends to the *seeded* file — "append, don't clobber");
  settings patch the seeded `settings.json`; the staged agent-config artifact must exist before the session
  `Launch`'s in-container monitor reads it. Pin this pipeline order. **The artifact's location is conveyed by
  convention** (D92): the agent layer compiles its path into the neutral `ProcSpec` (an arg or env var) so the
  in-container session process + monitor find it — the artifact is produced (agent), staged (envsetup), and
  read (in-container) by three parties, so its path must be an explicit contract, not an implicit default.
- **Atomicity / failure.** Staging happens **before** the neutral container runs, so a *partial* stage (some
  secrets written, settings patched, context appended, then a failure) must **not** let the box boot against
  half-staged contents — staging is **fail-closed + cleaned up** (don't launch; remove the partial secrets dir).
  Mirrors netpolicy's fail-closed.
- **The three env-bearing contracts need a stated rule.** Env lives in `ProvisionSpec.Env` (container base,
  substrate), `EnvSpec` resolved-env (agent runtime: config + profile + caller overlay, envsetup), and
  `ProcSpec.Env` (the per-process launch). Rule: envsetup resolves the agent runtime env and the agent-layer
  **compiles it into `ProcSpec.Env`** at launch; `ProvisionSpec.Env` is the base it layers over; caller overlay
  wins. The resolved **model** rides the agent command (via `ModelFlag`) compiled into `ProcSpec`, *not* an env
  var, unless an agent declares an env-based model.
- **Credential-at-rest ([DF43](findings-unresolved.md)/DF39).** The at-rest concern is **not** a default
  baseline warning. On the Docker/Launch path (E3) no host file is staged at all; on a single-user/ephemeral
  host the staged credential is the user's own `0600` file. **seatbelt/tart keep staged secrets on disk for
  the sandbox lifetime** and must surface this in integrator documentation (not a CLI warning); that handling
  is the per-principal `SecretsStagingDir` embedder knob. The secure-secrets build (DF38) is the durable fix
  for all backends.

## Cross-references

- **Decisions:** [D84](../decisions/working-notes.md) (substrate — envsetup is its dual),
  [D88](../decisions/working-notes.md) (the carve — host-side staging off the entrypoint),
  [D89](../decisions/working-notes.md) (the agent layer produces the `EnvSpec`); the credential what/who is
  [D63] (caller `Env`, zero ambient reads, `SecretsStagingDir`); this layer's own entry **D91**.
- **Findings:** envsetup is the home of [DF38](findings-unresolved.md) (secure credential delivery) and
  [DF39](findings-unresolved.md) (the `$HOME` credential bleed) — both seamed here, builds deferred.
- **Consumer:** control-eval — the metered-JV-key + adversarial-agent case is the driver for the secure-secrets
  seam (a real key crossing into an untrusted sandbox is exactly envsetup's membrane).
