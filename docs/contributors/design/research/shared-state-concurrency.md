# Shared-state concurrency: daemon vs daemonless, and file-locking wisdom

**Purpose.** Back the persistence-helper decision ([persistence-helper.md](../persistence-helper.md),
D87): can multiple independent tools (CLI + MCP) safely share file-locked per-sandbox state, or is a
central arbitrating daemon load-bearing? **Verdict: file-locked multi-tool access is sound *inside a
specific envelope*, and yoloAI sits inside it — largely because of decisions already made (single doc
per domain, atomic-rename, low contention). A daemon stays an optional future embedding, not a
requirement.** Verified 2026-06-15; sources inline.

## Daemon vs daemonless — the arbiter's complexity doesn't vanish, it moves

- **Docker's daemon buys correctness by being the single in-memory writer** — clients call in over a
  socket; `dockerd` serializes all container/image/state mutation (one source of truth, no
  multi-writer races). Cost: a privileged **root SPOF that must be running**. Tellingly the industry
  then *split* it (containerd + per-container `shim`) so the **state-owner is decoupled from the
  process-supervisor** — an admission you want those separate. [docs.docker.com/get-started,
  /engine/security, /engine/daemon/live-restore; moby/moby#30225 (design proposal, not shipped-code
  cert); docker.com blog 1.11]
- **Podman went daemonless and it works at scale — but not for free.** Multiple `podman` processes
  coordinate shared state with no central process, via **(a)** a bespoke SHM lock subsystem (built
  because naive *file* locking *leaked an FD per container* — containers/podman#1235) and **(b)** a
  transactional state DB (BoltDB → **SQLite** default in 4.8). The central-arbiter complexity moved
  into locks + a DB; it didn't disappear. [containers/podman#13058, #1235; pkg.go.dev libpod/lock/shm]
- **The documented pain of daemonless coordination** (the crux): a **fixed, exhaustible lock pool**
  with operator ceremony (`podman system renumber`; the `num_locks=2048` wall — observed-in-issues,
  not doc-stated); `/dev/shm` cleared on reboot → boot-id mismatch → `podman system migrate`;
  deadlock-hangs under lock contention; and **SQLite did not cure concurrency, it traded the failure
  mode** — with no daemon each process opens its own connection with an exclusive lock + forced
  `fsync`, serializing writes, so under contention/slow storage you get `database is locked`. The
  auto-migration itself shipped race bugs. [podman#16119, #14195, #20313, #22463 (verbatim), #28216]
- **Nuance — daemonless ≠ no service.** Podman offers an **on-demand, socket-activated, *ephemeral***
  API service (terminates after inactivity), *not* a mandatory persistent root daemon — the hedge
  that regains a serialization point when contention demands it, without the SPOF. [podman-system-service]

## File-locking wisdom (local FS, cooperating processes)

- **Use `flock(2)`, never `fcntl`/`F_SETLK`.** POSIX record locks have a silent-corruption footgun:
  the lock is released when *any* fd to the file is closed (a library that opens/reads/closes the file
  drops your lock) and they don't inherit across `fork` as expected — the man page itself calls it
  "bad." `flock` binds to the open file description (survives `dup`/`fork`), releases on last close,
  and is portable across Linux/macOS (OFD locks `F_OFD_SETLK` fix `fcntl` but are Linux-only).
  [man7 F_SETLK, flock, F_OFD_SETLK; LWN 586904; apenwarr "file locking"]
- **`flock` self-cleans on crash** — the kernel drops the lock when the holder dies, eliminating the
  entire stale-lock problem class. *Lockfile/PID-file* schemes do **not** self-clean and are defeated
  by PID reuse (TOCTOU). Prefer kernel `flock`. [man7 F_SETLK, _exit; gavv.net/file-locks]
- **Atomic publication for lock-free reads** — `write-temp + rename(2)` is atomic on the same
  filesystem; readers `open()` without locking and always get a complete file. Constraints: temp file
  must be in the **same directory** (cross-FS `rename` → `EXDEV`); for crash-durability do
  **`fsync(temp) → rename → fsync(dir)`** (skipping the pre-rename fsync is the real ext4
  zero-length-file data-loss bug). [man7 rename; LWN 457667, 323169]
- **No multi-file atomic commit with plain file locks.** If one operation must update several files
  consistently, a crash leaves inconsistent cross-file state and you can deadlock acquiring multiple
  locks — the cleanest reason people reach for SQLite. [sqlite.org/atomiccommit; danluu.com/file-consistency]
- **SQLite is the canonical embedded multi-process answer — but its hard limit is the network
  filesystem.** "POSIX advisory locking is known to be buggy or even unimplemented on many NFS
  implementations… do not use SQLite for files on a network filesystem"; WAL additionally needs
  same-host shared memory. So SQLite does **not** escape the NFS problem — it's *worse* there.
  [sqlite.org/lockingv3, /wal, /howtocorrupt]
- **The envelope where advisory file locks suffice** (where `dpkg`, `pacman`, `flock(1)` live): **low
  write contention · whole-resource operations · single host + local FS · cooperating processes · no
  need for change notifications.** Leave that envelope (network FS, multi-file atomic invariants, high
  contention, push notifications) → put a single-writer process/daemon or SQLite in front.
  [Thompson single-writer; danluu.com/deconstruct-files; man7 inotify, fcntl_locking]

## Application to yoloAI

- **We are in the envelope, by construction.** Single-doc-per-domain dodges the multi-file-atomicity
  limit; the data dir is local; one-agent-per-sandbox is low-contention; cooperating tools are all
  ours. So **JSON + `flock` + atomic-rename is the right, daemon-free choice** — SQLite would add a
  CGO/dep (against the "just the binary" ethos) and *also* fails on NFS.
- **Two real risks to handle:** (1) **network filesystem** — networked `$HOME` is real; advisory
  locking breaks → corruption. Detect and warn/refuse (DF36). (2) **Don't cargo-cult Podman's SHM lock
  pool** — their `num_locks` wall came from a fixed pool optimized for thousands of high-churn
  containers; a **per-sandbox `flock` file** (kernel-managed, unbounded) has no such wall at our scale
  (dozens of sandboxes).
- **The handle keeps the engine swappable.** JSON now; if we ever leave the envelope, the storage
  backend sits *behind* the handle and can become SQLite without touching a component or tool.
- **Daemon stays optional and deferred.** Because the library owns the coordination and tools are
  thin, file-locks-today / daemon-tomorrow is a choice *below* the library API — daemonless by default
  (no SPOF, no root daemon: Podman's win) with an on-demand service as the future hedge for
  low-latency push at scale (Podman's exact landing).
