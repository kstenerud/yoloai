# Substrate interface — the agent-free isolated-environment layer

**Status:** Design converged 2026-06-14 (design conversation), not yet implemented. The target
surface for the **substrate** layer of [plans/public-layering.md](plans/public-layering.md) —
shaped as-if-public, to live behind `internal/` until the promotion move. Resolves
[Q103](questions-resolved.md); informs [DF31/DF32/DF33](findings-unresolved.md). Backed by
[research/container-init-delineation.md](research/container-init-delineation.md).

**One-line definition.** A *substrate* is an isolated environment in which processes *can* run —
possibly several, possibly none in particular — with primitive ways to move bytes across its
boundary. It is the bottom rung: it knows nothing of agents, diff/apply, sessions, or PTYs.

## The model (the decisions behind the surface)

1. **Durable reality, cheap handle.** The named environment is the durable thing; it outlives the
   process that made it (yoloAI's defining trait — `yoloai new` exits, the box runs on). A *handle*
   is a cheap, re-acquirable controller. Construction is a **factory**, not a method on the handle:
   `Provision` (bring a new one into being) / `Open` (re-acquire an existing one by name). `Destroy`
   acts on the reality, so it invalidates **every** handle to that Identity forever (`ErrGone`).

2. **Two tiers, not three — mechanism vs policy.** The substrate provides process *mechanism*
   (launch, signal, wait, streams, and exit-code *as a reported fact*). All *policy* — does exit 7
   matter and to whom, kill vs wait, restart, ordering — is the **caller's**. "Supervision" is not a
   layer; it is caller policy applied to substrate mechanism. The agent layer is just *the specific
   caller yoloAI ships*, with its own policy.

3. **Status = liveness only (Q103).** Substrate `State ∈ {Provisioned, Running, Suspended,
   Stopped}`. It has **no** `Active`/`Idle`/`Done`/`Failed` — those are the agent layer interpreting
   `State` + a process's `ExitStatus` (Done/Failed = relabel of exit 0 / non-zero) + the monitor
   (Active vs Idle, the one thing only an agent-watcher can know). A launched process's exit is a
   per-process fact (`Process.Wait`), never substrate state.

4. **Process count is free; supervision is the boundary.** The substrate may `Launch` N processes
   and does only **Tier-1 hygiene** (reap orphans so they don't zombie; kill remaining processes on
   `Destroy`). It never does **Tier-2 supervision** (restart policy, ordering, backoff, health-driven
   restart). "Multiple agents + a second tool in one box" is fine; the line is *supervision*, not
   *count*.

5. **Keep-alive is the reaper, backend-native (`KeepAliveModel`).** The thing that holds the
   environment open is the same thing that reaps what runs in it — one responsibility. VM backends
   get it free (the guest OS init); containers need a thin neutral init (`tini` / `docker --init`,
   already the project's chosen reaper) — and the agent session must **stop being** that init
   (DF31). Declared as a `KeepAliveModel` capability, the `FilesystemLocality` way: a semantic
   property, never an "is this Tart?" check.

6. **Re-open the environment by name; never re-open a process by pid.** Grabbing a raw pid across a
   disconnect is neither useful nor OS-grade — you cannot re-`wait()` a reaped process, and you reach
   a daemon through *its* interface (`tmux attach` via tmux's socket), not its pid. So the `Process`
   handle is live **only within the launching call**; there is no `OpenProcess`. Re-acquisition
   splits into refinements reached through *interfaces*: **live streams / reattach** → a broker
   (tmux/dtach), which is a *consumer* that `Launch`es the broker inside the box and drives it via
   `Exec`; **durable "did the principal exit, code N"** → a persisted status channel maintained by an
   upper layer (persisting exit codes for later query is the systemd/Tier-2 behavior we decline).
   *(This is why tmux exists at all: the agent CLI exposes no reattach interface of its own, so we run
   it under a broker that does.)*

7. **Channels are emergent; the substrate provides ground + doors.** Sockets and fifos and status
   files live on the **filesystem**; ports live on the **network**. The substrate *is* an isolated
   filesystem + network + process space, so it needs no socket/fifo/port vocabulary — processes build
   channels on the ground it provides. Communication *inside* is free and invisible to it; the
   substrate mediates only the **host ↔ substrate boundary**, through a small fixed set of doors:
   `Mount` (provision-time), `PutPath`/`GetPath`, `Exec`, and `Network` (ports). No `Dial(pathInside)`
   convenience (that teaches it IPC); no dynamic post-provision channel attach (`Mount` is
   provision-time only).

8. **Principal stays out.** `Identity` is an opaque, caller-chosen name unique within a backend.
   Multi-tenant principal namespacing (D62) is an upper-layer concern that may *encode* itself into
   the name; the substrate never knows it exists.

9. **`ProvisionSpec` is agent-free.** Image, mounts, resources, network, isolation, env — and no
   agent command / ready-pattern / idle config. Those live in the agent layer's `ProcSpec` when it
   `Launch`es the agent (the schema half of DF33).

10. **`Suspend`/`Resume` are cap-gated** (VM backends); unsupported backends return `ErrUnsupported`
    rather than carrying a separate optional interface.

## The interface

```go
// ───── Construction (a factory; substrates outlive their creator) ─────
// New(ctx, kind, hostCfg) (Backend, error)  — registry maps a backend kind to a Backend.
type Backend interface {
    Caps() Caps                                       // static caps — known before any substrate exists
    Provision(ctx, ProvisionSpec) (Substrate, error)  // bring a NEW substrate into being → handle
    Open(ctx, Identity) (Substrate, error)            // re-acquire a handle to an EXISTING one
}

// ───── The substrate handle (durable reality, cheap re-acquirable view) ─────
// After Destroy, every handle to this Identity errors forever (ErrGone).
type Substrate interface {
    Identity() Identity
    Caps() Caps                                       // what THIS substrate can do — adapt without backend checks

    State(ctx) (State, error)                         // Provisioned | Running | Suspended | Stopped
    Ready(ctx) (bool, error)                          // up AND able to accept work (≠ merely Running)

    Start(ctx) error
    Stop(ctx) error
    Suspend(ctx) error                                // cap-gated (Caps.SupportsSuspend) else ErrUnsupported
    Resume(ctx) error
    Destroy(ctx) error                                // final; invalidates all handles to this Identity

    PutPath(ctx, hostSrc, dst string) error           // bytes in  — the filesystem boundary
    GetPath(ctx, src, hostDst string) error           // bytes out

    Launch(ctx, ProcSpec) (Process, error)            // N allowed; substrate tracks+reaps, never supervises
    Exec(ctx, ProcSpec) (ExecResult, error)           // convenience = Launch + Wait + capture
}

// ───── The process handle (mechanism only; live only within the launching call) ─────
type Process interface {
    ID() ProcessID
    Streams() Streams                                 // THIS process's stdin(w)/stdout(r)/stderr(r)
    Signal(ctx, os.Signal) error
    Wait(ctx) (ExitStatus, error)                     // block for exit + code; the CALLER decides what the code MEANS
}

// ───── Value types ─────
type Identity   struct { Name string }                // opaque, unique-within-backend; principal encoded by the caller, unseen here
type State      int                                   // Provisioned | Running | Suspended | Stopped
type ExitStatus struct { Code int; Signaled bool; Signal int }
type ExecResult struct { Stdout, Stderr []byte; Exit ExitStatus }
type Streams    struct { Stdin io.Writer; Stdout, Stderr io.Reader }

type Caps struct {                                    // declared SEMANTIC properties — never mechanistic ("IsTart")
    FilesystemLocality Locality                       // HostSide | SandboxSide
    KeepAlive          KeepAliveModel                 // GuestOSInit | ContainerInit | HostKeepAlive
    Isolation          []IsolationMode
    SupportsOverlay    bool
    SupportsSuspend    bool
    SupportsPorts      bool
}

type ProvisionSpec struct {                           // substrate-level ONLY — zero agent fields (DF33)
    Name      string
    Image     ImageRef
    Mounts    []Mount                                 // the filesystem boundary, wired at provision
    Resources ResourceLimits
    Network   NetworkSpec                             // ports = wiring (here); egress allowlist = netpolicy refinement
    Isolation IsolationMode
    Env       []EnvVar
}

type ProcSpec struct {                                // what to run, how
    Argv  []string
    Env   []EnvVar
    Cwd   string
    User  string
    TTY   bool                                        // request a pty; a RICH reattachable session is a refinement
    Stdin bool
}
```

## In-container provisioning the substrate owns (the entrypoint carve — DF41/DF42)

Provisioning is not only the host-side container shell; today `entrypoint.py` does **agent-free root work
in-container** that the D88 carve would otherwise orphan (verified — see [DF41](findings-unresolved.md)). The
substrate claims the two filesystem/identity pieces (it is the agent-free environment layer):
- **UID/GID remap** — retarget the in-container agent user to the host uid/gid + `chown` the bind-mounted tree,
  so the agent can read/write host-mounted files. Pure environment hygiene.
- **The in-container overlay mount** (`mount -t overlay`, with the VirtioFS→tmpfs fallback) — `:overlay`'s
  filesystem realization. **No layer owns this today** ([DF42](findings-unresolved.md): it is inline Python, no
  Go ownership, no explicit unmount); the substrate owns it as *filesystem provisioning* (copyflow operates on
  the result; the mount is the substrate giving the container its filesystem). The substrate must also own the
  **explicit unmount** on teardown (today implicit-via-destroy; the carve must not lose it).

The other two entrypoint operations re-home elsewhere: `isolate_network` → **netpolicy** (its `ip-filter`
strategy); the secrets read + `.secrets-consumed` handshake → **envsetup**. Under the carve these become
Go-driven steps (via `Launch`/`Exec`) over the neutral keep-alive, run in the backend's locality
([backend-topology.md](backend-topology.md): container / VM-guest / host).

## Deliberately NOT at this level (the boundary is the point)

- **Restart / backoff / ordering / `WaitForAll`** → caller policy (orchestration).
- **`Done` / `Failed` / `Active` / `Idle`** → agent-layer interpretation of `State` + `ExitStatus` + monitor.
- **Live reattach + scrollback** → session refinement (broker like tmux), a *consumer* of `Launch`/`Exec`.
- **Durable exit-status for a reconnecting client** → a persisted status channel, upper-layer.
- **Diff / apply / baselines** → copyflow refinement, built on `PutPath`/`GetPath`.
- **Runtime egress-allowlist mutation** → netpolicy refinement (configures `NetworkSpec`).
- **Agent command / ready / idle** → the agent layer's `ProcSpec`.
- **`Dial`/socket/fifo/IPC vocabulary**, dynamic channel hot-plug → not provided; build on the filesystem + network the substrate already is.

## How it reshapes today's `runtime.Backend`

- **`Backend` (factory)** ⇐ the construct/open slice of today's name-keyed `runtime.Backend`.
- **`Substrate` (handle)** ⇐ **new at the runtime layer.** Today per-instance ops are name-keyed
  methods; the only instance handle (`yoloai.Sandbox`) is agent-aware and lives up in the library.
  This pulls a clean, agent-free handle *down* to the substrate. (Resolves the Q106 naming worry: the
  substrate handle is `Substrate`; `yoloai.Sandbox` stays the agent-aware product handle — different
  names, different layers, no collision.)
- **`Process` (handle)** ⇐ **new.** Today `Exec` is one-shot and the agent is PID 1 (DF31/DF32). This
  is the reshape that yields the agent-free, multi-process launch primitive.
- **`Caps`** ⇐ today's `BackendDescriptor`/`BackendCaps` + `FilesystemLocality`, plus the new
  `KeepAliveModel`.

## Decided since (D85)

- **Q104 — persistence.** The substrate's `environment.json` is **agent-free** (consistent with §9):
  it persists only substrate facts. Agent config (`AgentType`/`Model`/`HasPrompt` + agent-launch
  settings) lives in an **agent-owned sidecar** (`agent.json`), via `store`'s sudo-safe IO. Not an
  opaque payload. Generalizes: each layer persists its own facts; the substrate record sheds all
  non-substrate fields. Versioned reshape (v2→v3). See [D85](../decisions/working-notes.md).
- **Q105 — foundation boundary.** `config.Layout`/`HostEnv` stay **internal**. The substrate's
  construction takes **narrow, edge-resolved inputs** — a small substrate-scoped paths value (the ~6
  dirs it uses) + injected curated host-tool env — never the fat aggregate. Parse-don't-validate at
  the public boundary. See [D85](../decisions/working-notes.md).

## Still open (not blocking the surface)

- **The persisted-status channel** (for durable exit/"done") is named here but its shape is the
  agent/upper layer's design, not the substrate's.
- **Shape-time:** per-purpose host-tool env keysets in the backend vs central in internal `HostEnv`
  (D85 leans backend-declares-keysets, edge-supplies-values).
