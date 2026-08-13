> **ABOUTME:** Make `devcontainer.json` safe by default — refuse every request in it that would
> widen the sandbox boundary, list them all in one message, and let one per-invocation flag grant
> the whole list.

# Repo-request trust — balk and list, never filter and proceed

- **Status:** PLANNED — designed, no code.
- **Depends on:** —
- **Rides:** **breaking.** A repo whose `devcontainer.json` carries mounts, capabilities or
  `forwardPorts` stops creating until the flag is passed. See [D141](../../decisions/working-notes.md).

## The problem

`devcontainer.json` is auto-detected from the workdir, which is untrusted input, and it can ask for
things that reach outside the sandbox. Today each category is handled differently and **none of the
three behaviours was ever decided** — they accreted:

| Request | Today | Where |
| --- | --- | --- |
| `mounts` | filtered, remainder granted | `devcontainer.go:152-195` (`FilterMounts`) |
| `runArgs --cap-add` | 17-cap denylist, remainder granted | `devcontainer.go:303-321`, `:333-365` |
| `runArgs --privileged` | refused on principle | `prepare_archetype.go:238-252` |
| `forwardPorts` / `appPort` | granted silently | `devcontainer.go:128-145` → `prepare_archetype.go:271-286` |
| `runArgs --cpus` / `--memory` | gap-fill only | `prepare_archetype.go:214-235` |
| `containerEnv` / `remoteEnv` | backfill only, CLI wins | `prepare_archetype.go:253-267` |
| `postCreateCommand` and siblings | granted | `create.go:684-708` |

The first two are the silent degradation [D138](../../decisions/working-notes.md) retired, surviving
in a corner that decision did not sweep. A **denylist of escape-enabling capabilities cannot be
complete by construction**, and its own comment concedes the shape was chosen "so that benign,
non-escalating caps in a devcontainer still work" — an availability argument standing in for a
safety one. The `--privileged` row is the one place the codebase already does the right thing, and
this plan generalises it rather than inventing anything.

## The line

**Refuse what widens the sandbox boundary; allow what happens inside it.**

Refused: `mounts`, `runArgs --cap-add`, `runArgs --privileged`, `forwardPorts`/`appPort`.

Allowed: lifecycle commands, `containerEnv`/`remoteEnv`, `--cpus`/`--memory`.

**Lifecycle commands are the load-bearing call.** They run as the unprivileged `yoloai` user —
`entrypoint.py:324` execs `sandbox-setup.py` through `gosu yoloai`, and `run_lifecycle_commands`
lives there — inside the sandbox, after the egress policy is applied. Running a repo's own code in a
disposable sandbox is the product's purpose; the agent will run it one prompt later regardless.
Refusing it would also make "safe by default" mean **devcontainer support is off by default**, since
nearly every real devcontainer has a `postCreateCommand`.

## Shape

One refusal, listing every offending request with its category and value, at the first `yoloai new`
that would apply them. One flag grants the whole list.

**The refusal message is the granularity.** The user sees every unsafe request enumerated before
deciding, so an all-or-nothing accept is already an informed one — which is why there are no
per-category flags to combine and document.

**The flag is per-invocation and must never become a persisted config key.** A persisted
`trust_repo: true` in `defaults/config.yaml` would reproduce the daemon failure immediately on the
CLI: set once, silently applied to every sandbox afterwards, including the repo the agent edited
last week. Unpersistable now is free; retrofitting it after a config depends on it is breaking.

## Order of work

1. **Collect instead of apply.** During archetype expansion, gather boundary-widening requests into
   a structured list — kind, value, and which file asked — rather than merging them into `opts`.
   The four collection sites are the rows above; `expandArchetype` (`prepare_archetype.go:117`) is
   the single funnel they all pass through.
2. **Refuse in `prepareSandboxState`, before the destructive replace.** Same insertion point as the
   mode-capability validation in [network-mode-reshape.md](network-mode-reshape.md): after
   `create.go:240` and before `replaceSandboxIfNeeded` (`:242`), which calls `launch.Teardown` and
   destroys the user's existing sandbox, and before Phase 2 (`:253`, `:266`) copies the whole
   workdir. Note the ordering constraint this creates: archetype expansion currently runs at
   `create.go:313`, *inside* Phase 1, so the collected list is available at `:240` — but only
   because expansion precedes it. Anything that moves expansion later breaks the refusal's position.
3. **Add the flag.** CLI on `yoloai new`/`run`, plus a matching field on `SandboxCreateOptions` so
   the library door has the same control — the door the plan for the network modes found had no
   guard at all. Assert it is absent from `store.Environment` so it cannot be persisted.
4. **Delete `FilterMounts` and `dangerousRunArgCaps`.** The refusal needs to know only *that* a
   request exists, never whether it is on a list. Deleting them is what stops the two mechanisms
   from drifting apart, and it is why this is a simplification rather than an addition.

## Tests (rule 10)

One per behavior change, each red on revert. `prepareSandboxState` runs end to end against the
existing `fakeRuntime` in `create_prepare_test.go`, untagged and inside `make check`, and the
devcontainer fixtures in `archetype_resolution_test.go` are the model.

- a `devcontainer.json` with `mounts` → create refuses, and the error names the mount
- one with `runArgs: ["--cap-add=SYS_PTRACE"]` → refuses
- one with a capability **not** on the old denylist (e.g. `--cap-add=CHOWN`) → refuses. This is the
  test that pins the denylist's removal; the previous two would pass with the denylist still in place.
- one with `forwardPorts` → refuses
- one with **only** a `postCreateCommand` → **succeeds**. The guard against over-refusing, and the
  test that fails if someone later moves lifecycle commands to the refused side without a decision.
- with the flag set → all of the above are granted
- the flag does not survive into `environment.json`

## Open

- **Do granted requests still get the mechanical checks?** Granting should remove the *refusal*, not
  duplicate-container-path or overlap detection, which are correctness rather than safety. Not settled.
- **Should granted devcontainer mounts become aux dirs?** They would then inherit dangerous-path
  refusal, overlap detection and the tiering that `--dir` has and raw mounts do not
  (`prepare_dirs.go:166-205`). Attractive, and a larger change than this plan.
- **`--cpus`/`--memory` are on the allowed side** on the grounds that they limit rather than grant.
  A repo asking for more memory than the host has turns into a create failure rather than an
  escalation — worth a transparency bullet, not a refusal.
- **The `mounts:` config key** is a separate open question: it is fully subsumed by profile
  `directories:` (`ProfileDir{Path, Mode, Mount}` → `opts.AuxDirs`, with every guard), and its
  original purpose — getting on-disk credentials into the sandbox — is now served by `/run/secrets`,
  the home-seed mounts (`mounts.go:147-183`, which copy to a staging dir and mount the copy),
  `agent_files`, the Keychain, and brokering. None of them touch the key.

## Surfaces to sweep (rule 2)

- **Shipped text:** `docs/GUIDE.md` (the devcontainer section and the config-key table at `:681`),
  `internal/cli/helpcmd/help/security.md`, `README.md` if it mentions devcontainer support.
- **Contributor docs:** `design/environments.md` (the archetype pipeline), `code-map.md` (gated —
  `FilterMounts` and `dangerousRunArgCaps` are named there), `principles/security-principles.md`,
  `backend-idiosyncrasies.md`.
- **Code:** `archetype/devcontainer.go`, `create/prepare_archetype.go`, `create/create.go`,
  `cli/lifecycle/{new,run}.go`, `sandbox_options.go`.
