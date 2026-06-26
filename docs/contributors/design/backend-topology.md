# Backend topology — where the agent, PID 1, and the monitor actually run

**Status:** Verified against the code 2026-06-24 (design-review remediation, [D92](../decisions/working-notes.md)).
A shared reference for the layer specs — substrate ([D84](../decisions/working-notes.md)), session
([D88](../decisions/working-notes.md)), netpolicy ([D90](../decisions/working-notes.md)), envsetup
([D91](../decisions/working-notes.md)) — so the carves reason about the *six* real backends, not a two-bucket
"Linux containers vs seatbelt/tart" approximation.

**The unifying fact.** All six backends launch the **same** `sandbox-setup.py`, which creates a tmux `main`
session, sends the agent command into the pane, and spawns `status-monitor.py`. So **the agent and the monitor
are always co-located with the process that runs `sandbox-setup.py`** — the carve's logic (demote
`sandbox-setup.py` from PID-1-init to a Go-driven `Launch`'d process under a neutral keep-alive) is **uniform**.
Only *where that process runs* and *how it's launched* differ per backend, and those differences are exactly
what the substrate's `KeepAliveModel` / `FilesystemLocality` properties abstract.

| Backend | PID 1 / top process | Agent + monitor run | Locality | How launched |
|---|---|---|---|---|
| **docker** | `entrypoint.sh` in the Linux container | Linux **container** | sandbox-side (container) | image ENTRYPOINT chain → `gosu yoloai … sandbox-setup.py` |
| **podman** | `entrypoint.sh` in the (rootless) container | Linux **container** | sandbox-side (container) | as docker, `--userns=keep-id` (non-root entrypoint) |
| **containerd** | `entrypoint.sh` in a **Kata microVM guest**; host-side = the `containerd-shim-kata-v2` | **VM guest** (Kata) | sandbox-side (guest) | `ctr` task; backend supports only `vm`/`vm-enhanced` |
| **tart** | host = `tart run` (VM supervisor); agent = tmux child in the VM | **macOS VM guest** | sandbox-side (guest) | `tart exec <vm> … sandbox-setup.py`; workdir via VirtioFS + rsync |
| **apple** | `entrypoint.sh` in a **per-container Apple VM** | **VM guest** (Apple) | sandbox-side (guest) | `container create` (no cmd → image ENTRYPOINT); reuses the docker image |
| **seatbelt** | the **`sandbox-exec` process on the host** | **on the host** (SBPL-confined; no container, no VM) | **host-side** | `sandbox-exec -f profile.sb python3 sandbox-setup.py seatbelt …` |

## Corrections this verifies

- **"seatbelt/tart run tmux+agent on the host" is wrong for tart** (and containerd/apple). Only **seatbelt** is a
  host process; **tart/containerd/apple run the agent inside a VM guest**. Earlier session/netpolicy spec prose
  said "seatbelt/tart … on the host" — true for seatbelt, false for the rest.
- **containerd is a Kata microVM guest**, not a namespace container; the host-side process is the Kata shim.
- **apple is "both container and VM"** — OCI semantics inside a per-container VM; its `KeepAliveModel` is the
  guest-OS-init kind (the VM's init), reusing the docker entrypoint chain unchanged.

## Carve implications (what each layer must carry)

- **The carve is uniform** (same `sandbox-setup.py` demoted everywhere). The **only structurally divergent case
  is seatbelt** — the `HostKeepAlive` model: there is **no "inside" to launch into**, the agent/broker/monitor
  are *host* processes in the substrate's own process tree. So session §"Launch unit / liveness" needs a
  **host-process variant**: substrate liveness is the host process group, not a container/guest.
- **VM-guest backends (containerd/tart/apple):** the neutral-PID-1 + `Launch` applies *inside the guest*; the
  host-side process (Kata shim / `tart run` / the `container` apiserver) is the substrate's keep-alive handle.
- The **agent-free root work currently fused in `entrypoint.py`** (UID remap, the in-container overlay mount,
  network isolation, the secrets read + consumed-marker handshake) is the same in every container/guest backend
  and is what the carve must re-home per-layer — see [DF41](findings-unresolved.md)/[DF42](findings-unresolved.md).
