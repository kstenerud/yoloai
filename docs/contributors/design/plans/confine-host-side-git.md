# Plan: confine all host-side work-copy git (apple, seatbelt, broken-metadata probe)

ABOUTME: Close the pre-existing copy-mode RCE surface on backends where work-copy git still
ABOUTME: runs on the host with filters/textconv/fsmonitor live (apple, seatbelt) + the probe.

Status: **DESIGN — pre-existing security hole, prioritized.** The 2026-06-29 fix
([copy-mode-git-rce.md](copy-mode-git-rce.md)) closed the RCE for docker/podman/containerd
and tart, but left **apple and seatbelt** running work-copy git host-side, and left a
host-side `git status` in the broken-metadata recovery path on **all** backends. This plan
closes those. It is the invariant that [copy-mode-history.md](copy-mode-history.md) depends
on; it should land first or concurrently.

## The vulnerability (verified in code, 2026-07)

Copy/diff/apply copies a user repo into an agent-writable work copy. The work copy always
contains a `.git` (even the fresh `yoloai baseline`), and the agent can write its
`.git/config` and `.gitattributes`. Git executes shell commands defined in repo-local config
— `filter.<name>.clean`/`.smudge`, `diff.<name>.command`/`.textconv`, `core.fsmonitor` — not
only from `.git/hooks/`. So any git operation that runs **outside confinement** against that
work copy is arbitrary code execution wherever it runs.

Host-side git applies only one neutralization: `GitHardeningArgs()` =
`-c core.hooksPath=/dev/null` (`runtime/runtime.go:325`). That disables hooks **but not
filters, textconv, or fsmonitor** (`internal/git/git.go:96` prepends it to every host-side
call). This is intentional — those drivers must run for diff correctness on repos using Git
LFS / git-crypt — and it is *safe only when git runs in-confinement*. Where git runs
host-side, it is host RCE.

**Dispatch:** `runtime.GitRunsInConfinement(rt)` (`runtime/runtime.go:312`) =
`FilesystemLocality == LocalitySandboxSide || GitExecInConfinement`. `git.NewSandbox` routes
to in-confinement exec when true, else `hostExec` (`internal/git/git.go:55-61`).

### Current truth (code-verified)

| Backend | `GitRunsInConfinement` | Work-copy git runs | Status |
|---|---|---|---|
| docker / podman / containerd | ✅ `GitExecInConfinement` | in-container | safe |
| tart | ✅ `LocalitySandboxSide` | in-VM | safe |
| **apple** | ❌ | **host-side, filters live** | **RCE — undocumented** |
| **seatbelt** | ❌ | **host-side, filters live** | **RCE — profile also too loose** |

Plus one path on **all** backends: the broken-metadata recovery probe
(`status.ProbeWorkData` → `workprobe.DetectChanges`, `internal/orchestrator/status/status.go:135`)
runs `git status --porcelain` on the work copy via `git.NewHost` — host-side, filters/fsmonitor
live — when a sandbox's `.meta` is missing/corrupt.

### Threat model

An attacker who lands an instruction in the agent (poisoned README, issue comment, any content
the agent reads) has the agent write a `filter.x.clean` (or `core.fsmonitor`, or a
`diff.x.textconv` + matching `.gitattributes`) into the work-copy `.git`. The next
yoloAI-initiated `diff`/`add`/`status`/`format-patch` on apple/seatbelt then executes the
payload **on the macOS host** — outside every sandbox boundary. Both affected backends are
macOS-only ("lighter tier"), which is exactly where users may assume the boundary is weaker
and keep the host valuable.

This is **not** the history feature's problem — it is present today with the fresh baseline
`.git`. Preserving history (the companion plan) must gate on the invariant this plan
establishes so it does not widen the surface.

## The invariant

> No git operation touches an agent-writable `.git`/work-tree outside the agent's confinement.

Concretely: make `GitRunsInConfinement(rt)` true for apple and seatbelt, and remove (or
neutralize) the one host-side probe. Three fixes, ordered by effort.

### Fix 1 — apple: wire the exec it already has (low effort)

Apple runs a VM and **already exposes `Exec` via `container exec`** (`runtime/apple/apple.go:291`);
it simply never implemented `GitExecer`. The work copy is mounted into the VM, so git can run
there against the same path.

- Implement `runtime.GitExecer` on the apple `Runtime`, mirroring tart
  (`runtime/tart/tart.go:546-560`): `GitExec(ctx, name, _, workDir, args...)` →
  `Exec(ctx, name, append([]string{"git","-C",workDir}, args...), user)`.
- Set `GitExecInConfinement: true` in apple's capabilities (`runtime/apple/apple.go:60-68`).
- Assert `var _ runtime.GitExecer = (*Runtime)(nil)`.

Effect: `GitRunsInConfinement(apple)` becomes true; all work-copy git routes into the VM,
where a planted filter fires inside the agent's confinement. This closes apple with the same
mechanism already proven for tart — the filter tools must be present in the apple base image
(same requirement as the container backends today).

### Fix 2 — seatbelt: `sandbox-exec`-wrapped git + profile tightening (higher effort)

Seatbelt has no VM/container to exec into; the agent is a host process under
`sandbox-exec -f <SBPL>`. "In-confinement git" therefore means **running git itself under an
SBPL profile**, so any filter it spawns inherits that confinement. Two parts, both required —
the wrapper alone is not safe under today's profile.

**2a. GitExecer via sandbox-exec.** Implement `GitExecer` for seatbelt that runs
`sandbox-exec -f <git-profile> git -C <workDir> <args…>` on the host and captures stdout
(same output contract as the other backends). yoloAI (the orchestrator) is unconfined and can
initiate this; the git op and its filter children are confined. Set the capability so
`GitRunsInConfinement(seatbelt)` becomes true.

**2b. A dedicated, tight git profile (the F5 fix).** Today's agent profile emits
`(allow process-exec)`, `(allow process-fork)`, and `(allow mach-lookup)` unrestricted
(`runtime/seatbelt/profile.go`, `writeProfileHeader`) — macOS sandbox-escape vectors, so even
a wrapped git could be escaped from. Rather than loosen the *agent* profile (the agent
legitimately needs broad exec), author a **separate, minimal profile for the git op**:

- `(deny default)`; grant `file-read*`/`file-write*` scoped to the work copy + its `.git` and
  read-only access to the system/tool paths git and filters need.
- **Deny `mach-lookup`** (allowlist only the minimal services git requires, if any) — closes
  the primary escape vector.
- Allow `process-exec`/`process-fork` **only within tool directories** (`/usr/bin`,
  Homebrew/toolchain prefixes, the profile image's tool dirs) so legitimate filters (git-lfs,
  git-crypt) still run, but a payload cannot exec arbitrary host binaries outside that scope
  or write outside the work copy. This bounds a malicious filter to container-equivalent blast
  radius: it can run tools and read/write the work copy, but cannot escape to the host.
- Network follows netpolicy, not this profile. Note the edge: LFS *smudge* needs network, but
  the diff/format-patch path exercises *clean* (local) not smudge, so the diff path does not
  need network; a smudge-on-checkout path that needs network is an out-of-scope follow-up.

The dedicated git profile is preferable to reusing the agent profile because git needs far
less than a full coding agent, so it can be tightened aggressively without risking agent
functionality. (Tightening the agent profile's mach-lookup/process-exec is a worthwhile
separate hardening, but is not required to close *this* hole once git has its own profile.)

### Fix 3 — broken-metadata probe: strict host-side hardening (all backends)

`ProbeWorkData`/`DetectChanges` runs `git status --porcelain` host-side via `git.NewHost`
during recovery, when the sandbox may be stopped/unrecoverable (so we cannot always exec into
it). `git status` does not smudge/clean, so filters are not the risk here — but `core.fsmonitor`
runs a configured command, and that is agent-controllable.

Add a **strict hardening arg set** for host-side status-class operations that also neutralizes
fsmonitor (and, defensively, any read-time driver status could invoke):
`-c core.fsmonitor=false` (alongside the existing `-c core.hooksPath=/dev/null`). Apply it to
the probe path specifically. This is cheap, needs no runtime, and closes the recovery-path
exposure on every backend. (Diff/format-patch keep the current, filter-preserving hardening
because they need drivers for correctness and now run in-confinement.)

> Note — baseline creation at create time (`prepare_dirs.go`) also runs host-side, but on the
> **user's own** repo config before the agent has run (not agent-controlled), so it is the same
> trust level as the user running `git` themselves and is out of scope for this RCE fix.

## Sequencing / priority

1. **Fix 1 (apple)** — low effort, closes a fully-open, undocumented RCE. Do first.
2. **Fix 3 (probe)** — cheap, all-backends, no runtime dependency. Do alongside Fix 1.
3. **Fix 2 (seatbelt)** — the profile-authoring work; largest, macOS-only. Do next.

Fixes 1 and 3 are Linux-testable in part (probe hardening) and remove the largest exposure
quickly; Fix 2 requires real macOS iteration on the SBPL profile.

## Testing

- Extend the malicious-filter integration test (`internal/orchestrator/integration_test.go:502`,
  which asserts a planted `filter.x.clean` does **not** create a host marker) to run on **apple**
  and **seatbelt**. Post-fix, the host marker must never appear.
- fsmonitor probe test: plant `core.fsmonitor=<marker-writing-cmd>`, corrupt `.meta`, run the
  recovery probe, assert the marker is not created (host-side) after the strict-hardening fix.
- Seatbelt profile: assert a filter payload under the git profile cannot (a) exec a binary
  outside the tool dirs, (b) write outside the work copy, (c) reach a denied mach service.
- macOS backends verified on real hardware (`make check` is Linux and does not exercise them);
  the probe hardening is unit-testable on Linux.

## Docs / decisions to update

- **Fix the stale claim in [copy-mode-git-rce.md](copy-mode-git-rce.md):** its status says the
  fix shipped for the container backends and lists seatbelt as the sole residual — it omits
  **apple** entirely, and does not note that host-side hardening leaves filters/textconv/fsmonitor
  live. Add apple; state the exact neutralization gap.
- Add a **decision entry** in `decisions/working-notes.md`: the invariant ("no git on an
  agent-writable repo outside confinement"), the three fixes, and the dedicated-git-profile
  choice for seatbelt.
- Record a **backend-idiosyncrasy** entry (`backend-idiosyncrasies.md`) for the seatbelt
  `sandbox-exec` mach-lookup/process-exec escape surface and the dedicated-profile mitigation.
- Update the capability/security matrix so apple/seatbelt confinement status is accurate.

## Open questions

- **Seatbelt filter tool discovery:** filters exec arbitrary tools; the profile allows
  tool-dir exec rather than enumerating binaries. Confirm the tool-dir allowlist covers the
  profile image's toolchain locations (Homebrew arm64 `/opt/homebrew`, system `/usr/bin`).
- **mach-lookup minimal set:** determine whether git/filters need *any* mach service under
  sandbox-exec, or if a blanket deny is viable. → `questions-unresolved.md`.
- **LFS smudge network** on the seatbelt git profile (see 2b) — defer unless a real repo needs
  smudge during diff.
