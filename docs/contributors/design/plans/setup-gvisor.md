<!-- ABOUTME: Design for an opt-in `yoloai system setup-gvisor` that installs/registers -->
<!-- ABOUTME: runsc in the macOS Docker VM, and the OrbStack /tmp constraint it must face. -->

# `yoloai system setup-gvisor` (macOS)

## Why this doc exists

`container-enhanced` (gVisor) now works on macOS hosts (D70) once `runsc` is installed
and registered in the Docker VM. But neither Docker Desktop nor OrbStack ships runsc, so
on a fresh macOS machine `--isolation container-enhanced` fails — and on OrbStack it fails
*even after* runsc is installed, because of a `/tmp` collision. This plan designs an
**opt-in** command (chosen over silent auto-handling and detect-only) that does the setup
explicitly, plus the diagnostics that guide users who haven't run it.

Status: **design.** Not implemented. One blocking decision (the OrbStack `/tmp` tradeoff)
is called out below and should be settled before building the VM-mutating part.

## What the command does (the safe core)

`yoloai system setup-gvisor` (macOS, docker backend), idempotent and reversible:

1. **Detect** the VM-backed daemon (OrbStack vs Docker Desktop via `docker info` context)
   and the VM architecture.
2. **Install runsc into the VM** (not the macOS host): download the matching-arch binary,
   verify its checksum, place it where the daemon can exec it.
   - *OrbStack:* writable VM rootfs; install via a privileged `--pid=host` helper that
     `nsenter`s the VM mount namespace (the same path used to verify this manually).
   - *Docker Desktop:* the LinuxKit rootfs is read-only — installing into a persistent,
     exec-able location is the open question (R-DD below); may not be feasible without a
     Docker Desktop extension.
3. **Register** runsc as a daemon runtime (`daemon.json` `runtimes`, with
   `--platform=systrap` so no nested virtualization is needed) and reload.
4. **Verify**: run `docker run --runtime=runsc … echo ok` and report success/failure with
   the real reason.

A `--remove` inverse and a dry-run/`--check` mode round it out. Everything is gated behind
the explicit command — yoloai never mutates the user's Docker VM on a normal `new`.

## The blocking constraint: OrbStack `/tmp`

gVisor hard-codes its sandbox chroot at `/tmp` and runs a mount-safety check that the
resolved path matches. OrbStack symlinks the VM's `/tmp → /private/tmp` (the macOS host
over virtiofs), so the check fails: `expected to open /tmp, but found /private/tmp`
(surfaces as `cannot read client sync file: EOF`). See `backend-idiosyncrasies.md`.

**There is no clean per-process workaround.** Investigated and ruled out:

- `TMPDIR` / runsc flags — the chroot path is hard-coded to `/tmp`, not configurable.
- Bind-mounting a real dir over `/tmp` or `/private/tmp` — the *symlink indirection* is
  what trips the check; remounting the target doesn't remove the symlink, and you can't
  bind-mount "over" the symlink itself.
- A private mount namespace — you'd have to replace the `/tmp` symlink with a real dir,
  which requires a global `unlink` (not namespaced) and breaks OrbStack's `/tmp` sharing.

So the only ways to make gVisor run on OrbStack are both unattractive as a default:

- **(a) Replace the VM's `/tmp` symlink with a real directory** (globally). Works, but
  breaks OrbStack's macOS-`/tmp` sharing — a side effect on the user's whole VM.
- **(b) `--TESTONLY-unsafe-nonroot`** in the runtime args — skips the chroot, but disables
  a gVisor security boundary. Unacceptable for a "secure isolation" mode.

**Decision needed:** for OrbStack, should `setup-gvisor` (a) offer the global `/tmp`
replacement behind an explicit confirmation + `--remove` restore, or (b) refuse OrbStack
and steer users to Docker Desktop? Recommendation: **(a) with a loud, specific
confirmation** (it's opt-in already, and reversible), but only after R-DD clarifies whether
Docker Desktop is even a working alternative. Docker Desktop, if installable, has a normal
`/tmp` and needs none of this.

## Diagnostics (ship regardless — already partly done)

Independent of the command, the failure paths now guide the user (implemented in D70
follow-up): the system check's runsc Fix step is OS-aware (macOS → "install in the VM, not
`/etc/docker/daemon.json`"), and `launch` annotates the two opaque macOS failures —
runsc-registered-but-missing, and the OrbStack `/tmp` chroot error — with actionable text.
`setup-gvisor` is what those hints should eventually point at.

## Open research

- **R-DD (Docker Desktop install):** can runsc be installed persistently and exec-ably in
  Docker Desktop's read-only LinuxKit VM (extension? `/var/lib`-backed path?), or is
  OrbStack the only supported macOS target for now?
- **Persistence across VM restarts/updates:** does an OrbStack `/usr/local/bin` install
  survive a VM restart / OrbStack upgrade, or must `setup-gvisor` be re-run (and should
  `system check` detect a drifted/removed binary and say so)?

## References

- D70 — `docs/contributors/decisions/working-notes.md` (enhanced allowed on macOS; daemon-location runsc check).
- `docs/contributors/backend-idiosyncrasies.md` — the OrbStack `/tmp` gVisor chroot collision.
- `docs/GUIDE.md` — gVisor setup (Linux + macOS).
- [podman-gvisor.md](podman-gvisor.md) — the sibling backend; a Podman Machine setup story would parallel this.
