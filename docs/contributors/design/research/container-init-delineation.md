# Container init / keep-alive: where the substrate stops and the OS begins

**Purpose.** Decide how a yoloAI *substrate* (an isolated environment where processes *can*
run, not necessarily anything in particular) keeps itself "up", without reinventing an
operating system, an init, or an orchestrator. Feeds [public-layering.md](../plans/public-layering.md)
(the substrate boundary), [Q103](../questions-unresolved.md) (status = liveness vs activity),
and [DF31](../findings-unresolved.md) (the agent session is currently PID 1).

**Thesis.** A minimal isolated-environment substrate must not drift into Tier-2 (process
supervision) or Tier-3 (orchestration). The container/VM ecosystem already provides everything
below that line; the entire path to Kubernetes is a museum of distinctions a substrate should
*borrow as concepts and refuse to reimplement*. The good news (§4): for three of our backends
the substrate boundary is already clean, and a fourth already uses the right pattern in one mode.

## 1. The spectrum (we invent none of it)

| Tier | What it does | Provided by | yoloAI |
|---|---|---|---|
| 0 — isolation | namespaces / cgroups / VM | kernel, runc, Virtualization.framework, Kata | consume |
| 1 — PID 1 hygiene | reap zombies, forward signals, hold the box open | `tini`, `dumb-init`, `docker --init`, a guest OS init, k8s `pause` | **adopt** |
| 2 — process supervision | start/restart/order *N* services, backoff, readiness | `s6-overlay`, `supervisord`, `runit` | **must NOT build** ← the ouroboros line |
| 3 — orchestration | init-containers, sidecars, probes, restart policy, scheduling | Kubernetes, compose | out of scope |

## 2. The PID 1 problem and the thin-init fix (Tier 1)

A normal application process running as PID 1 misbehaves in two specific ways:

- **Signals.** The kernel special-cases PID 1: if it hasn't registered a handler for a signal,
  the kernel does **not** apply the default action — the signal is dropped (the `SIGNAL_UNKILLABLE`
  rule in `kernel/signal.c`). So an app as PID 1 that doesn't explicitly handle SIGTERM ignores it;
  `docker stop` then waits the grace period (**10 s** on Linux, configurable) and SIGKILLs.
  [dumb-init README; Biederman patch to `kernel/signal.c`; `docker container stop` docs]
- **Zombies.** Orphaned children are reparented to PID 1, which must `wait()` on them. A normal
  app doesn't, so zombies accumulate and can exhaust the process table (PID starvation).
  [dumb-init README; phusion "PID 1 zombie reaping" post]

The fix is a **thin init** as PID 1 that spawns the real work as a child (restoring default signal
dispositions), forwards signals, and reaps zombies:

- **`tini`** — tiny static C binary (~10 KB dynamic, <1 MB static); `ENTRYPOINT ["/tini", "--"]`.
- **`dumb-init`** — Yelp's equivalent; small static C binary.
- **`docker run --init`** — runtime-side, no image change; **the bundled init is `tini`**. It is
  **off by default** (opt-in per-container, or daemon-wide via `"init": true`).
  [krallin/tini; Yelp/dumb-init; `docker run` + `dockerd` docs]

**The dev-container "exec-in" pattern.** Since a container dies when PID 1 exits, tools that want a
long-lived box to `docker exec` into make PID 1 a neutral keep-alive. The Dev Container spec's
`overrideCommand` replaces the image command with `/bin/sh -c "while sleep 1000; do :; done"`
(POSIX; the GNU `sleep infinity` / `tail -f /dev/null` idioms are equivalent but `sleep infinity`
is absent from some BusyBox images). Caveat: a bare `sleep`/`tail` PID 1 does **not** reap or
forward signals — pair it with a thin init (`tini -- sleep infinity`, or `--init`) to get both.
[containers.dev spec; VS Code dev-container docs]

## 3. What we must not build (Tier 2), and the k8s gotchas (Tier 3)

### Tier-2 supervisors — the "fat container" line
- **s6-overlay** — real reaping + multi-service supervision + ordered init + log multiplexing (~650 KB). A genuine init replacement.
- **supervisord** — manages multiple processes but is **explicitly not an init**: its docs never claim reaping, and *"if a process… creates its own child processes, supervisord cannot kill them"* (needs the `pidproxy` shim). Needs a Python runtime.
- **runit** — small, reliable per-service supervision; regarded as *"overkill for containers."*

Docker's own guidance: *"Each container should have only one concern"* — but *"it's not a hard and
fast rule"*; one **concern** ≠ one **process** (Celery/Apache legitimately spawn workers). The
anti-pattern is multiple *concerns/aspects* in one box, not a process count. [Docker best-practices
+ multi-service docs]

### Kubernetes — lessons from the painful path (borrow the concept, not the machinery)

- **The `pause` container = the canonical "neutral init."** Every pod runs a tiny do-nothing
  container (`for(;;) pause();`) that holds the pod's shared namespaces and, when PID-namespace
  sharing is on, is **PID 1 that reaps zombies**. Its whole virtue is being *too dumb to become an
  orchestrator*. This is the precedent for a neutral keep-alive that is deliberately **not** the
  workload. [Ian Lewis, "The Almighty Pause Container"]
- **Init containers** — run-to-completion setup *before* the main process exists; separation of
  concerns. Lesson: "prepare, then run" is a distinct phase; anything long-running does **not**
  belong in it.
- **The sidecar saga (the headline gotcha).** For ~4 years sidecars were just long-running
  containers, which caused concrete pain — above all **"the Job never completes"**: a sidecar that
  doesn't exit hangs the pod forever; plus startup/shutdown ordering races. It took **KEP-753**
  (alpha 1.28 → beta 1.29 → **GA 1.33**) to formalize native sidecars (init containers with
  `restartPolicy: Always`, killed after the main containers exit). **Lesson:** conflating "runs
  alongside" with "blocks completion" is *exactly* the drift-into-orchestrator failure mode — and
  it is the trap our status-monitor + "relaunch the agent" logic sits closest to.
- **Probes** — liveness/readiness/startup are the platform *polling* the workload, not the workload
  self-reporting. Mental model for our monitor: a **probe/observer with no authority to drive
  lifecycle**, not part of init.
- **`shareProcessNamespace`** — sharing a PID namespace gets you pause-as-PID-1 reaping, but at a
  real cost: `/proc` and `/proc/$pid/root` leak across containers (*"secrets are protected only by
  filesystem permissions"*), and signalling siblings needs `SYS_PTRACE`. **Lesson:** get reaping
  from a thin init *inside one isolation boundary*; never punch holes between boundaries to reap.
- **`restartPolicy` / backoff** — Jobs retry with exponential backoff (10s → 20s → 40s …, capped at
  6 min; `backoffLimit` default 6). The very existence of these tunables is the evidence:
  **robust supervision is a deep problem.** A substrate should run the process, surface that it
  exited, and let a human/outer system decide on restart — the moment you add backoff curves you're
  building the orchestrator you set out not to be.

## 4. Our backends today — the asymmetry that shrinks the task

Audited 2026-06-14. "Keep-alive" = what holds the environment open; "PID 1 coupling" = whether the
environment's existence is welded to the agent session.

| Backend | What keeps the environment up | Agent session is PID 1? | Coupling |
|---|---|---|---|
| **Docker / Podman** | `sandbox-setup.py` (entrypoint.sh → entrypoint.py → execs it) blocks on `tmux wait-for yoloai-exit` | **yes** | **tight** (DF31) |
| **Tart** (macOS VM) | guest macOS `launchd`; `sandbox-setup.py` runs as a child via `tart exec` | no — guest OS is PID 1 | loose ✅ |
| **containerd** (Kata VM) | guest init (systemd); entrypoint chain runs *inside* but the guest OS holds the VM | no — guest OS is PID 1 | loose ✅ |
| **Apple** (per-container VM) | guest init; same chain inside | no — guest OS is PID 1 | loose ✅ |
| **Seatbelt P1** (bare) | **`tail -f /dev/null`** under the sandbox-exec profile | n/a (host process) — neutral | loose ✅ |
| **Seatbelt P2** (full) | `sandbox-setup.py` blocking (Docker-like) | n/a — but agent-coupled | tight |

Pointers: docker `resources/entrypoint.sh:10` → `entrypoint.py:442` → `monitor/sandbox-setup.py`
(blocks ~`:1336`); tart `tart.go:310-425` (`tart run`), exec `:475`; containerd `lifecycle.go:396`,
exec `exec.go:57`; apple `apple.go:232`, exec `:291`; seatbelt bare keep-alive `seatbelt.go:317`
(`tail -f /dev/null`), full `:319`.

**Two facts fall out:**
1. **The VM backends are already a clean substrate** in your sense — the guest OS keeps the box "up
   with nothing particular running"; the agent is just a child process. We'd be insane to add our
   own init there.
2. **The neutral-keep-alive pattern already exists in-repo** — Seatbelt P1 uses `tail -f /dev/null`.
   So the only entanglement is **Docker/Podman (+ Seatbelt P2)**, where the agent-session script is
   the keep-alive. That is DF31, and the remedy is the well-precedented Tier-1 pattern, not new code.

## 5. Recommendation — the delineation

- **The substrate contract is "the environment is up" + the I/O primitives (`exec`, streams,
  `transfer`). How it stays up is backend-native and never a yoloAI invention.** VMs use their guest
  OS init; containers use a thin neutral init; Seatbelt uses a keep-alive process. This is the
  anti-ouroboros boundary: a uniform *contract*, backend-specific *mechanism*.
- **For containers: PID 1 should be a neutral reaper that holds the box open** (`docker --init` /
  `tini`, or a `tini -- sleep infinity` keep-alive — the k8s `pause` / Seatbelt-P1 `tail` pattern),
  and **the agent session becomes a process launched via `exec`** into an already-live environment —
  a refinement, not the substrate's reason to exist. This is the DF31 fix.
- **The status-monitor is a probe/observer** (k8s probe model): it watches and reports, it does not
  drive lifecycle. Keep it out of PID 1 and out of init.
- **Model the asymmetry as a declared property, not a backend check** — e.g. a `KeepAliveModel`
  {guest-OS-init, container-init, host-keepalive} in the same spirit as `FilesystemLocality`, so the
  substrate contract states *that* the environment stays up without each layer asking *which backend*.
- **Bright lines NOT to cross (Tier 2/3):** process supervision of multiple services, restart
  policies / backoff curves, dependency ordering, health-driven restart, anything sidecar-lifecycle
  shaped. Run a process, surface its exit; restart is an outer decision.
- **"Done/Failed" (a principal process exited with code N)** is a thin *run-supervision* notion above
  the substrate (the Q103 middle tier) — it must stay "launch one process, report its exit," never
  grow into auto-restart/backoff. The substrate itself owns only liveness; exit codes are per-`exec`
  results (already `ExecResult.ExitCode`), not substrate state.

## 6. Size estimate

**Small, and mostly subtraction.** Three backends need no change (guest OS init already does it);
the neutral keep-alive already ships for Seatbelt P1. The work is to give Docker/Podman a neutral
PID 1 and launch the agent via exec (DF31) — the pattern is standard, externally proven, and already
present elsewhere in the repo. The danger is not implementation effort; it is *scope drift* into
Tier 2 — which this doc exists to forbid.

## Sources

PID 1 / init: krallin/tini, Yelp/dumb-init, phusion "Docker and the PID 1 zombie reaping problem",
`docker container stop` / `docker run` / `dockerd` docs (docs.docker.com), `kernel/signal.c`
(Biederman LKML patch). Supervisors: just-containers/s6-overlay, supervisord.org/subprocess,
smarden.org/runit, ahmet.im "minimal init process for containers", Docker best-practices +
multi-service docs. Dev containers: containers.dev json_reference, code.visualstudio.com remote
advanced-containers. Kubernetes: ianlewis.org "The Almighty Pause Container", kubernetes.io
init-containers / sidecar-containers (v1.28 blog) / probes / share-process-namespace / Jobs docs,
KEP-753 (sig-node/753-sidecar-containers). Verification flags from the research pass: the kernel
symbol `sig_task_ignored` is cited indirectly (via `kernel/signal.c` + patch, not `signal(7)`); the
"supervisord leaks zombies" claim is community-sourced (consistent with its docs' silence on init
duties + the documented grandchild-kill limitation); the combined "keep-alive + thin-init does both"
is a sound synthesis of two verified facts, not one quotable sentence.
