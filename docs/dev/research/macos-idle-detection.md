# macOS Process Idle Detection Research

Research into macOS-specific techniques for detecting when an interactive TUI process (AI coding agent) is waiting for user input vs actively working. Conducted 2026-03.

**Context:** On Linux, `/proc/PID/wchan` gives high-confidence idle detection: `n_tty_read` = waiting for terminal input, `do_epoll_wait` = event loop (check network to disambiguate). On macOS, `/proc` does not exist. This document evaluates every known alternative.

**Bottom line up front:** No single macOS technique matches Linux `/proc/PID/wchan` fidelity. The best practical approach combines `sysctl KERN_PROC` for wait channel (`e_wmesg` field showing `ttyin`/`select`/`kqueue`) with `lsof`/`nettop` for network activity detection. This combination works without root for same-user processes, has acceptable overhead for 2-second polling, and can distinguish terminal-input-wait from network-wait with medium confidence. The `sysctl` approach is the closest macOS equivalent to Linux's `/proc/PID/wchan`.

---

## 1. `ps -o wchan` / `sysctl KERN_PROC` (Wait Channel)

### How it works

macOS `ps -o wchan` displays the symbolic name of the kernel wait channel — the event/function the process is sleeping on. Under the hood, `ps` reads this from `sysctl` via `CTL_KERN / KERN_PROC`, which returns a `kinfo_proc` structure. The relevant fields are:

- `kp_eproc.e_wmesg` — wait channel message string (char[8], max 7 chars + null)
- `kp_proc.p_wchan` — wait channel address (numeric, less useful)

The `e_wmesg` field is the BSD-layer wait message, set by the `msleep()` kernel function when a thread blocks. This is the macOS equivalent of Linux's `/proc/PID/wchan`.

### Known wait channel values on macOS/BSD

| `e_wmesg` value | Meaning | Relevance |
|---|---|---|
| `ttyin` | Blocked reading from TTY/PTY (terminal input) | **The target — waiting for user input** |
| `ttyout` | Blocked writing to TTY | Not relevant |
| `select` | Blocked in `select(2)` syscall | Event loop — needs network check |
| `kqueue` | Blocked in `kqueue(2)` event loop | Event loop — needs network check |
| `pselect` | Blocked in `pselect(2)` | Similar to `select` |
| `sbwait` | Waiting for socket buffer data | Network I/O — actively working |
| `piperd` | Blocked reading from pipe | Child process communication |
| `pipewr` | Blocked writing to pipe | Child process communication |
| `pause` | Waiting for signal | Idle/sleeping |
| `wait` | Waiting for child process (`wait(2)`) | Actively working |
| `nanslp` | In `nanosleep(2)` | Sleeping |
| (empty) | Running or no wait message set | Unknown state |

### Can it distinguish terminal-read from network-wait?

**Yes, partially.** `ttyin` is a strong signal for "waiting for terminal input" — equivalent to Linux's `n_tty_read`. `select`/`kqueue` are ambiguous (could be waiting for network or idle event loop), requiring a supplementary network check, same as Linux's `do_epoll_wait`.

### Stability across macOS versions

These are BSD-layer wait messages that have been stable across Darwin/XNU for decades (inherited from FreeBSD). The `msleep()` wait message strings are set by kernel subsystem code and rarely change. **However, the `e_wmesg` field is limited to 7 characters** (WMESGLEN=7), so longer kernel function names get truncated. The values listed above (`ttyin`, `select`, `kqueue`, etc.) are within this limit and are stable.

**Caveat I cannot verify:** Whether modern macOS (Sequoia 15, Sonoma 14) still populates `e_wmesg` for all process states. Some reports suggest it may be empty more often than on FreeBSD. This needs testing on actual macOS hardware.

### Permissions

Works without root for same-user processes. The `sysctl KERN_PROC` interface is available to unprivileged users for processes they own.

### Overhead

Negligible. Single `sysctl` call, no subprocess needed. Can be called directly from Python via `ctypes` or Go via `syscall.Sysctl`. Even `ps -o wchan= -p PID` as a subprocess is fast enough for 2-second polling.

### Practical usability rating

**Medium-high confidence** when `e_wmesg == "ttyin"`. **Medium confidence** when `e_wmesg` is `select`/`kqueue` (requires network activity check to disambiguate). **Unknown** when empty.

### Programmatic access (no subprocess)

```python
import ctypes
import ctypes.util

libc = ctypes.CDLL(ctypes.util.find_library("c"))

# sysctl([CTL_KERN, KERN_PROC, KERN_PROC_PID, pid], ...)
# Returns kinfo_proc struct; e_wmesg is at a known offset.
# Alternatively, just shell out to: ps -o wchan= -p PID
```

For a Go implementation, use `golang.org/x/sys/unix` with `SysctlKinfoProcSlice`.

---

## 2. DTrace / dtruss

### Current state on macOS with SIP enabled

DTrace on macOS is **severely restricted** when System Integrity Protection (SIP) is enabled (default on all modern Macs):

- **Cannot trace system executables** in protected paths (`/bin`, `/usr/bin`, `/System`)
- **Can trace user-owned, non-system executables** — this is the key finding
- SIP blocks the `DTRACE_UNRESTRICTED` flag; only system-signed tools bypass it
- `dtruss` (macOS's strace equivalent) wraps DTrace and inherits the same restrictions

### What works without disabling SIP

DTrace **can** trace non-system executables that the user owns. Since AI coding agents (node, python, etc.) are installed in user-accessible paths or inside containers, DTrace could theoretically trace their syscalls to detect `read(fd=0, ...)` (stdin read indicating idle).

However:
- DTrace still requires **root** (sudo) even for non-system executables when SIP is enabled
- The `-p PID` flag to attach to a running process requires root
- Writing and loading DTrace scripts requires root

### Selective SIP disable

`csrutil enable --without dtrace` in Recovery Mode enables DTrace while keeping other SIP protections. This is documented but:
- Requires booting into Recovery Mode
- Not practical for end-user deployment
- On Apple Silicon, requires reducing security from "Full Security" to "Reduced Security"

### Verdict

**Not practical for yoloAI.** DTrace requires root even with SIP's relaxed mode for non-system executables. Requiring users to disable SIP or grant root for idle detection is unacceptable. DTrace is a debugging/profiling tool, not a production monitoring solution on modern macOS.

### Verified on

General behavior verified across Sonoma (14.x) and Sequoia (15.x) based on multiple sources. The SIP restrictions have been in place since El Capitan (10.11, 2015) and have only gotten stricter.

---

## 3. `proc_pidinfo` / `libproc`

### What it provides

macOS's `libproc` provides process inspection functions that are the closest equivalent to reading `/proc` on Linux:

- **`proc_pidinfo(pid, PROC_PIDLISTFDS, ...)`** — Lists all file descriptors for a process. Returns an array of `proc_fdinfo` structs, each containing fd number and fd type.
- **`proc_pidfdinfo(pid, fd, PROC_PIDFDVNODEPATHINFO, ...)`** — For vnode (file) fds: returns the file path (e.g., `/dev/ttys001` for a terminal).
- **`proc_pidfdinfo(pid, fd, PROC_PIDFDSOCKETINFO, ...)`** — For socket fds: returns socket domain, type, protocol, and connection state.

### Can it detect blocked-on-stdin?

**No.** `proc_pidinfo` can tell you *what* fd 0 points to (e.g., a PTY device) and *what type* of fd it is, but it **cannot tell you whether the process is currently blocked reading from it**. There is no read-state or blocking-state field in the returned structures.

This is a critical limitation compared to Linux's `/proc/PID/wchan` which directly tells you the process is in `n_tty_read`.

### What it CAN do for idle detection

1. **Confirm fd 0 is a TTY:** Verify that stdin points to a PTY device (`/dev/ttys*` or `/dev/pty*`), confirming the process is an interactive terminal program.
2. **Enumerate network connections:** Using `PROC_PIDFDSOCKETINFO`, list all TCP/UDP sockets and their states (ESTABLISHED, LISTEN, etc.) — equivalent to `lsof -i -p PID` but without spawning a subprocess.
3. **Count active connections:** Check for ESTABLISHED TCP connections to determine if the agent is communicating with an API server.

### Permissions

`proc_pidinfo` works for **same-user processes without root**. It uses a less restrictive security model than `task_for_pid()`. This is confirmed by psutil's implementation on macOS which uses `proc_pidinfo` to avoid `task_for_pid` permission issues.

**Exception:** Some flavors of `proc_pidinfo` may return `EPERM` for processes with hardened runtime or special entitlements, but this would not apply to AI coding agents running in a user session.

### Overhead

Very low. Direct syscall (`__proc_info` under the hood), no subprocess. Suitable for 2-second polling. `psutil` uses this for all macOS process inspection.

### Practical usability

**Useful as supplementary signal** — can enumerate network connections to distinguish idle event loop from active API communication. Cannot directly detect "blocked on terminal read." Must be combined with `sysctl KERN_PROC` wait channel.

### Note on documentation

`libproc.h` is **undocumented** by Apple. The functions exist in the SDK and are used by system tools (lsof, Activity Monitor) but have no official documentation. The API surface has been stable for many years but Apple makes no guarantees.

---

## 4. `kevent` / `kqueue` Monitoring

### Can we watch another process's file descriptors?

**No.** `kqueue` can only monitor file descriptors that the calling process owns. You cannot create a kqueue filter on fd 0 of a different process — you would need the actual file descriptor, which requires `task_for_pid()` to access (root-only).

### What kqueue CAN do

- **`EVFILT_PROC`** — Monitor process-level events: `NOTE_EXIT`, `NOTE_FORK`, `NOTE_EXEC`. Works for same-user processes. Useful for detecting agent process exit but not idle state.
- **File monitoring** — `EVFILT_VNODE` on files you open yourself. Could monitor `status.json` for changes (event-driven instead of polling). But we already poll this file.

### macOS-specific limitation for TTY

macOS does **not** support using `kqueue` or `poll(2)` to monitor `/dev/tty`. This is a known macOS kernel limitation — only `select(2)` works for monitoring TTY devices. This quirk means we can't use kqueue to watch the agent's terminal for activity even if we had access to the fd.

### Verdict

**Not useful for idle detection.** Cannot monitor another process's file descriptors. The `EVFILT_PROC` filter could supplement exit detection but we already handle that via tmux's `pane_dead`.

---

## 5. Mach APIs (`task_info`, `thread_info`)

### Thread state information

The Mach layer provides thread inspection via `thread_info()` with `THREAD_BASIC_INFO`:

```c
struct thread_basic_info {
    time_value_t user_time;
    time_value_t system_time;
    integer_t    cpu_usage;
    policy_t     policy;
    integer_t    run_state;     // TH_STATE_RUNNING, TH_STATE_WAITING, etc.
    integer_t    flags;
    integer_t    suspend_count;
    integer_t    sleep_time;
};
```

Thread run states:
- `TH_STATE_RUNNING` (1) — running normally
- `TH_STATE_STOPPED` (2) — stopped
- `TH_STATE_WAITING` (3) — waiting normally
- `TH_STATE_UNINTERRUPTIBLE` (4) — in uninterruptible wait
- `TH_STATE_HALTED` (5) — halted at clean point

### Can it distinguish terminal-read from network-wait?

**No.** `thread_basic_info` tells you a thread is in `TH_STATE_WAITING` but does **not** tell you *what* it's waiting on. There is no wait-event or wait-channel field in the Mach thread info structures exposed to userspace. The kernel internally knows the wait event (it's stored in `thread->wait_event`) but this is not exposed via the Mach API.

### Permission requirements

**Requires `task_for_pid()` to get the task port**, which requires:
- Root privileges, **or**
- `com.apple.security.cs.debugger` entitlement, **or**
- Process is the calling process itself

On modern macOS with SIP enabled, `task_for_pid()` on other processes is restricted to root or the `procmod` group. This makes it impractical for a user-space monitoring tool.

### Could CPU usage heuristic work?

`thread_basic_info.cpu_usage` and `user_time`/`system_time` could theoretically indicate activity (high CPU = working, zero CPU = idle). But this doesn't distinguish "idle waiting for input" from "idle waiting for network response" — both show zero CPU while blocked.

### Verdict

**Not useful.** Requires root for cross-process inspection, and even with access, cannot distinguish the type of wait. Strictly less useful than the `sysctl KERN_PROC` approach which at least provides `e_wmesg`.

---

## 6. `lsof -p PID`

### What it provides

`lsof -p PID` lists all open files, sockets, and connections for a process. On macOS, it shows:
- File descriptor number and access mode (read/write/both)
- File type (regular, directory, character device, socket, pipe)
- For sockets: protocol, local/remote address, connection state (ESTABLISHED, LISTEN, etc.)
- For devices: device path (e.g., `/dev/ttys001`)

### Can it detect blocked-on-read?

**No.** lsof shows what files/sockets are open and their connection state, but not whether the process is currently blocked reading from any of them. Same limitation as `proc_pidinfo`.

### Permissions

On macOS, lsof shows **only your own processes** without root. This is sufficient for our use case since the monitoring process and the agent run as the same user.

### What it CAN do

- **Enumerate TCP connections:** `lsof -i TCP -p PID -sTCP:ESTABLISHED` lists active network connections. Equivalent to Linux's `/proc/PID/net/tcp` check.
- **Confirm stdin is a TTY:** Shows fd 0 pointing to `/dev/ttys*`.
- **Combined with wait channel:** If `sysctl` says the process is in `select`/`kqueue` and lsof shows no ESTABLISHED TCP connections, the process is likely idle.

### Overhead

**Moderate.** `lsof` spawns a subprocess and parses kernel data structures. On macOS, typical execution time is 50-200ms. For 2-second polling this is acceptable but not ideal. Using `proc_pidinfo` directly (via ctypes/cgo) would be faster but more complex.

### Practical usability

**Useful as network activity check.** Best combined with wait channel detection. For production use, prefer `proc_pidinfo` with `PROC_PIDFDSOCKETINFO` to avoid subprocess overhead.

---

## 7. Network Activity Detection

### Available tools on macOS (no root required)

| Tool | Subprocess? | Per-PID? | Shows traffic volume? | Root needed? |
|---|---|---|---|---|
| `lsof -i -p PID` | Yes | Yes | No (connection state only) | No (same user) |
| `nettop -p PID -l 1 -P` | Yes | Yes | Yes (bytes in/out) | No |
| `netstat -p tcp` | Yes | No (no PID filter) | No | No |
| `proc_pidinfo` + `PROC_PIDFDSOCKETINFO` | No (API) | Yes | No (connection state only) | No (same user) |
| `NetworkStatistics.framework` | No (API) | Yes | Yes | Unknown (private API) |

### `nettop` for traffic volume

`nettop` is the best option for measuring network traffic per process:

```bash
nettop -p PID -l 1 -P -j bytes_in,bytes_out -d
```

- `-p PID` — filter to specific process
- `-l 1` — logging mode, 1 sample, then exit (non-interactive)
- `-P` — per-process summary only
- `-j bytes_in,bytes_out` — only show byte counters
- `-d` — delta mode (show change since last sample)

This runs without root and provides per-process byte counters. Two samples 2 seconds apart with delta mode would show if the process has active network traffic.

**Overhead:** Spawns a subprocess. Single-sample mode (`-l 1`) completes quickly (sub-second). Acceptable for 2-second polling.

### `lsof -i` for connection enumeration

```bash
lsof -i TCP -p PID -sTCP:ESTABLISHED -Fn 2>/dev/null
```

Shows ESTABLISHED TCP connections. If none exist, the process has no active API connections. Fast output format (`-Fn`) minimizes parsing overhead.

### `proc_pidinfo` for zero-overhead network check

Using `proc_pidinfo(pid, PROC_PIDLISTFDS)` then `proc_pidfdinfo(pid, fd, PROC_PIDFDSOCKETINFO)` for each socket fd, you can enumerate connections without spawning any subprocess. This is what `lsof` does internally on macOS.

**Limitation:** Provides connection state (ESTABLISHED/LISTEN) but not byte counters. Cannot detect idle persistent connections (e.g., WebSocket with no traffic). For WebSocket detection, need `nettop` delta mode or accept the ambiguity.

### Verdict

**Network detection works well on macOS.** Combination of `proc_pidinfo` for fast connection check + `nettop` for traffic volume when needed. No root required.

---

## 8. `sysctl` Approach

### Relevant MIBs

- **`CTL_KERN / KERN_PROC / KERN_PROC_PID / pid`** — Returns `kinfo_proc` for a specific PID. Contains `e_wmesg` (wait channel) as discussed in section 1.
- **`CTL_KERN / KERN_PROC / KERN_PROC_ALL`** — Returns all processes. Not needed for single-PID monitoring.

### What `kinfo_proc` provides

Beyond `e_wmesg` (wait channel), the `kinfo_proc` structure also contains:
- `kp_proc.p_stat` — process state (SRUN, SSLEEP, SSTOP, SZOMB)
- `kp_proc.p_flag` — process flags
- `kp_proc.p_nice`, `p_priority` — scheduling info
- `kp_eproc.e_tdev` — controlling terminal device
- `kp_eproc.e_pgid` — process group ID

The `p_stat == SSLEEP` + `e_wmesg == "ttyin"` combination is the strongest signal for "waiting for terminal input" available on macOS without root.

### Undocumented but stable

The `KERN_PROC` sysctl MIB is not officially documented by Apple, but it has been present in every Darwin/XNU release and is used by `ps`, `top`, Activity Monitor, and other system tools. Breaking it would break all process listing tools on macOS.

### Programmatic access

Go: `golang.org/x/sys/unix.SysctlKinfoProc("kern.proc.pid." + strconv.Itoa(pid))`
Python: `subprocess.check_output(["ps", "-o", "wchan=", "-p", str(pid)]).strip()`
or via `ctypes` calling `sysctl` directly.

### Verdict

**Best available technique on macOS.** The `e_wmesg` field is the direct equivalent of Linux's `/proc/PID/wchan`. Combined with network activity detection, provides medium-high confidence idle detection.

---

## 9. Endpoint Security Framework

### Requirements

- **`com.apple.developer.endpoint-security.client` entitlement** — must be requested from Apple via the System Extensions Request Form
- **Must run as root** — ES clients must be root processes (Launch Daemon or System Extension)
- **Must be signed and notarized** — or SIP must be disabled
- **Cannot be distributed via Mac App Store**

### Available event types (~100)

ES provides NOTIFY and AUTH events for: process exec/fork/exit, file open/close/rename/unlink, socket connect/bind, mmap, signal delivery, and many more.

### Can it detect process idle state?

**No.** ES monitors discrete events (process creation, file access, network connections), not continuous process state. There is no ES event for "process started waiting on terminal input" or "process stopped waiting." ES is designed for security monitoring (blocking malicious operations), not performance/state monitoring.

### Verdict

**Not applicable.** Wrong abstraction level (event-driven security monitoring vs. process state polling). Also impractical due to requiring root, Apple entitlement, and notarization. Massive overkill for idle detection.

---

## 10. Other macOS-Specific Techniques

### 10a. `proc_pid_rusage` (resource usage)

`proc_pid_rusage(pid, RUSAGE_INFO_V4, &rusage)` returns detailed resource usage including disk I/O bytes, network bytes, CPU time, etc. Available without root for same-user processes.

**Relevance:** Could detect network activity by comparing `ri_bytes_sent`/`ri_bytes_received` between polls. Cumulative counters (not deltas), so you'd compute the delta yourself. **This is potentially better than `nettop` for traffic detection** — no subprocess, direct API call, per-process granularity.

**Limitation:** Cannot distinguish terminal-wait from other idle states. Supplementary to wait channel detection.

**Status:** Not verified whether `ri_bytes_sent`/`ri_bytes_received` specifically track the process's own network I/O or system-wide. Needs testing.

### 10b. `kdebug` / `ktrace`

macOS's kernel trace facility. Used by Instruments.app and `fs_usage`. Could trace syscalls including `read(0, ...)`.

**Limitation:** Requires root. The `kdebug`-based tools (`fs_usage`, `sc_usage`) all require sudo.

**Verdict:** Same problem as DTrace — requires root.

### 10c. Activity Monitor private APIs

Activity Monitor uses private frameworks (`ActivityMonitor.framework`) to get detailed process info. These are undocumented, unsigned, and could change with any macOS update.

**Verdict:** Not viable for production use.

### 10d. `ptrace(PT_DENY_ATTACH)` and security implications

macOS processes can opt out of debugging with `PT_DENY_ATTACH`. This doesn't affect our techniques (we don't use ptrace), but worth noting that some processes may also restrict `proc_pidinfo` access through hardened runtime.

### 10e. Polling `ps` output

A simple fallback: `ps -o wchan=,state= -p PID` returns wait channel and state in one call. Spawns subprocess but is robust and well-tested.

```bash
$ ps -o wchan=,state= -p 12345
ttyin  S
```

`S` = sleeping, combined with `ttyin` = waiting for terminal input. Simple, works everywhere, no root needed.

---

## Summary: Recommended macOS Idle Detection Strategy

### Primary detector: `sysctl KERN_PROC` wait channel

Read `kinfo_proc.kp_eproc.e_wmesg` via sysctl for the agent PID.

| `e_wmesg` | Interpretation | Confidence |
|---|---|---|
| `ttyin` | Waiting for terminal input = **idle** | High |
| `select`, `kqueue`, `pselect` | Event loop — check network | Medium (needs supplementary) |
| `sbwait` | Waiting for socket data = **working** | High |
| `wait` | Waiting for child = **working** | High |
| `pause`, `nanslp` | Sleeping = unknown context | Low |
| (empty) | Running or unknown | Unknown |

### Supplementary detector: Network activity check

When wait channel is ambiguous (`select`/`kqueue`):

1. **Fast check:** `proc_pidinfo` + `PROC_PIDFDSOCKETINFO` — enumerate TCP connections. No ESTABLISHED connections = idle event loop.
2. **Traffic check (if persistent connections exist):** `proc_pid_rusage` byte counter deltas, or `nettop -p PID -l 1 -P -d` for traffic volume. Zero traffic on persistent connections = idle.

### Fallback: `ps -o wchan=` subprocess

If direct sysctl access is too complex to implement initially, shell out to `ps -o wchan= -p PID`. Same information, subprocess overhead (~10ms), perfectly fine for 2-second polling.

### Confidence assessment

| Scenario | Linux confidence | macOS confidence | Gap |
|---|---|---|---|
| Process blocked on TTY read | High (`n_tty_read`) | **Medium-High** (`ttyin` in e_wmesg) | Small — `e_wmesg` may be empty on some versions |
| Process in event loop, no connections | High (`do_epoll_wait` + no TCP) | **Medium** (`select`/`kqueue` + no TCP) | Small |
| Process in event loop, with connections | Medium (TCP traffic check) | **Medium** (same approach, different APIs) | None |
| Process running (CPU active) | High (not in wchan) | **Medium** (e_wmesg may be empty) | Small |
| e_wmesg empty | N/A | **Unknown** — must fall back to output_stability | Gap |

### Key unknowns requiring testing on actual macOS hardware

1. **How reliably is `e_wmesg` populated on macOS Sequoia/Sonoma?** If it's frequently empty, the sysctl approach degrades to "unknown" and we fall back to weaker heuristics.

2. **Does `proc_pid_rusage` `ri_bytes_sent`/`ri_bytes_received` accurately track per-process network traffic?** If yes, this is better than `nettop` for traffic detection (no subprocess).

3. **What does `e_wmesg` show for Node.js processes (Claude Code, Codex) blocked in their event loop on macOS?** Likely `kqueue` (macOS uses kqueue instead of epoll), but needs verification.

4. **Tart VM considerations:** Inside a Tart VM running macOS, do these APIs work the same as on bare metal? Tart uses Apple's Virtualization.framework — it runs a real macOS kernel, so sysctl/libproc should work identically.

---

## Integration with Architecture Proposal

The existing architecture proposal (`idle-detection.md` section 3.4) notes that macOS lacks wchan. With this research, we can update the detector catalog:

### New detector: `wchan_macos` (Medium-High Confidence)

**How:** Read `e_wmesg` from `sysctl KERN_PROC` for the agent PID. Map `ttyin` to idle, `sbwait`/`wait` to active, `select`/`kqueue` to "check network."

**Applies to:** All agents on macOS (Seatbelt backend) and inside Tart VMs.

**Platform:** macOS only.

**Implementation:** Python status monitor calls `ps -o wchan= -p PID` (simple) or reads sysctl directly via ctypes (faster).

**Relationship to Linux wchan:** Same concept, slightly weaker due to 7-char truncation and potentially empty `e_wmesg`. Should share the same detector interface with platform-specific implementations.

### Updated detector selection (macOS column)

| Detector | claude (macOS) | gemini (macOS) | codex (macOS) | aider (macOS) | opencode (macOS) |
|---|---|---|---|---|---|
| `hook` | **primary** | - | - | - | - |
| `wchan_macos` | supplementary | **primary** | **primary** | **primary** | **primary** |
| `ready_pattern` | - | supplementary | supplementary | supplementary | - |
| `context_signal` | - | supplementary | supplementary | supplementary | supplementary |
| `output_stability` | - | fallback | fallback | fallback | fallback |

This is a significant improvement over the current state where non-Claude agents on macOS have no primary detector.

---

## Sources

- [Eitan Adler on BSD wchan values](https://blog.eitanadler.com/2010/09/proccess-kernel-states-wchan-in-ps.html)
- [ps man page - macOS (SS64)](https://ss64.com/mac/ps.html)
- [ps keywords - macOS (SS64)](https://ss64.com/mac/ps_keywords.html)
- [Using dtrace on macOS with SIP enabled (poweruser.blog)](https://poweruser.blog/using-dtrace-with-sip-enabled-3826a352e64b)
- [A brief history of SIP (Eclectic Light)](https://eclecticlight.co/2025/08/23/a-brief-history-of-sip/)
- [proc_pidinfo Rust library (GitHub)](https://github.com/mmastrac/proc_pidinfo)
- [libproc PROC_PIDLISTFDS example (GitHub Gist)](https://gist.github.com/amomchilov/661ea7fd85f1d60292ebaafce263293f)
- [Using libproc for ports on macOS (Apple Developer Forums)](https://developer.apple.com/forums/thread/728731)
- [psutil macOS AccessDenied issue (GitHub)](https://github.com/giampaolo/psutil/issues/883)
- [thread_basic_info (Apple/MIT Darwin docs)](https://web.mit.edu/darwin/src/modules/xnu/osfmk/man/thread_basic_info.html)
- [XNU thread_info.h (GitHub)](https://github.com/apple/darwin-xnu/blob/main/osfmk/mach/thread_info.h)
- [Who needs task_for_pid anyway? (newosxbook.com)](https://newosxbook.com/articles/PST2.html)
- [Endpoint Security documentation (Apple)](https://developer.apple.com/documentation/endpointsecurity)
- [ES entitlement (Apple)](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.developer.endpoint-security.client)
- [nettop man page (manpagez)](https://www.manpagez.com/man/1/nettop/osx-10.12.3.php)
- [macOS network monitoring (SANS ISC)](https://isc.sans.edu/diary/30160)
- [Complete macOS lsof guide (osxhub)](https://osxhub.com/macos-lsof-command-guide/)
- [lsof on macOS (Simon Willison)](https://til.simonwillison.net/macos/lsof-macos)
- [macOS /dev/tty polling limitation (nathancraddock.com)](https://nathancraddock.com/blog/macos-dev-tty-polling/)
- [sysctl kinfo_proc (Apple Developer Forums)](https://developer.apple.com/forums/thread/9440)
- [Emacs macOS process attributes bug (GNU)](https://lists.gnu.org/archive/html/bug-gnu-emacs/2021-05/msg01652.html)
- [Counting open fds on macOS (Zameer Manji)](https://zameermanji.com/blog/2021/8/1/counting-open-file-descriptors-on-macos/)
