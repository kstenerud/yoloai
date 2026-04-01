# Smoke Test V2: Base / Full Split

> **This is a design spec**, not a description of current state. See the
> [Implementation status](#implementation-status) section at the end for what
> exists today vs what remains to be built.

## Motivation

The current smoke test has no depth tiers: `smoketest` and `smoketest-full` differ only
in whether unavailable backends abort or warn. Both tiers run the same tests plus the
full backend matrix, making the "fast" path slow.

The deeper problem is tier confusion. Several tests in the smoke test don't need a real
agent at all and belong one or two tiers lower:

- `clone`, `files_exchange` — no agent required; `clone` can be set up with a manual
  work-copy write the same way `TestIntegration_DiffWithChanges` does
- `start_done_agent` — a tmux socket regression test, not LLM behavior
- `reset`, `exec`, `:rw`, aux dirs — already covered in `sandbox/integration_test.go`

Meanwhile, the CLI integration tests have real gaps: no `apply` end-to-end, no files
exchange round-trip, no clone, no start-after-done.

The overlay workdir is only tested in smoke despite not needing a real agent — it belongs
in `sandbox/integration_test.go` with a capability skip guard.

**Goal**: move everything that doesn't require a real agent to the integration tier.
Smoke tests should answer one question: *does the real agent work end-to-end across
backends?*

---

## Summary of changes

### Go integration tests (new/moved)

| Test | Location | Notes |
|------|----------|-------|
| `TestCLI_StartAfterDone` | `internal/cli/integration_test.go` | Moved from smoke `start_done_agent` |
| `TestCLI_FilesExchange` | `internal/cli/integration_test.go` | New — `files put/ls/get` round-trip |
| `TestCLI_Apply` | `internal/cli/integration_test.go` | New — `apply` end-to-end via CLI |
| `TestIntegration_Clone` | `sandbox/integration_test.go` | New — clone captures work-copy state |
| `TestIntegration_Overlay` | `sandbox/integration_test.go` | Moved from smoke; skip if no CAP_SYS_ADMIN |
| `TestIntegration_ReadOnlyMountVerified` | `sandbox/integration_test.go` | New — exec write to RO aux dir fails |
| `TestIntegration_CredentialInjection` | `sandbox/integration_test.go` | New — /run/secrets lifecycle + host cleanup |

Also add `testutil.WaitForStatus` helper to support `TestCLI_StartAfterDone`.
Signature accepts a `func(context.Context) (string, error)` status poller rather than a
`runtime.Runtime` directly — sandbox status is a higher-level concept than container
running/stopped state, and importing `sandbox` from `testutil` would create an import cycle.

### Smoke test (`smoke_test.py`)

- Add `--full` flag to select backend matrix width and test depth
- Remove: `start_done_agent`, `files_exchange`, `reset`, `overlay` (moved to integration tier)
- Restrict `clone` to full tier only (stays in smoke as T3)
- Base tier: `full_workflow` (docker + one VM) + `stop_start`
- Full tier: `full_workflow` on full matrix + `stop_start` on full matrix + `clone`
  (kept in full because it confirms agent-written changes survive a clone, not just
  mechanical clone behavior)

### Makefile

```makefile
smoketest: build
    python3 scripts/smoke_test.py --limited --debug $(SMOKE_ARGS)

smoketest-full: build
    python3 scripts/smoke_test.py --debug $(SMOKE_ARGS)
```

Currently uses `--limited` for the base tier (skip unavailable backends). When the
`--full` flag is implemented and `--limited` removed, this becomes:

```makefile
smoketest: build
    python3 scripts/smoke_test.py --debug $(SMOKE_ARGS)

smoketest-full: build
    python3 scripts/smoke_test.py --full --debug $(SMOKE_ARGS)
```

---

## Tier definitions

### Base (`smoketest`)

Runs with `--debug` (no `--full`). Skips unavailable backends rather than aborting.
Intended for developer local runs and nightly CI.

**Backend matrix** — docker + primary VM per platform:
- Linux: docker, containerd-vm
- macOS: docker, tart

**Tests**: `full_workflow`, `stop_start`, and `isolation_check` on each matrix backend.

Target wall-clock time: under 30 minutes on a warm machine with pre-pulled images. Docker
tests finish in ~5 minutes; containerd-vm (QEMU) dominates with 5–10 min per sentinel
wait and T2 requiring two waits plus restart overhead (~15–20 min on containerd-vm alone).

If the base tier consistently exceeds 30 minutes, consider running T2 on Docker only in
the base tier (with full-matrix T2 in the full tier). This would roughly halve base tier
wall time.

### Full (`smoketest-full`)

Runs with `--full`. Aborts if any configured backend is unavailable.
Intended for pre-release runs on the dedicated test machine.

**Backend matrix** — all backends per platform:
- Linux: docker, podman, docker-cenhanced, containerd-vm, containerd-vmenhanced
- macOS: docker, podman, seatbelt, tart

**Tests**: `full_workflow`, `stop_start`, and `clone` on the full matrix;
`isolation_check` on container backends only (Docker, Podman, containerd-vm).

---

## Backend matrix (data structures)

Replace the single `LINUX_BACKENDS` / `MACOS_BACKENDS` lists with four constants:

```python
BASE_LINUX_BACKENDS:  [docker, containerd-vm]
FULL_LINUX_BACKENDS:  [docker, podman, docker-cenhanced, containerd-vm, containerd-vmenhanced]

BASE_MACOS_BACKENDS:  [docker, tart]
FULL_MACOS_BACKENDS:  [docker, podman, seatbelt, tart]
```

All non-matrix tests use `DEFAULT_BACKEND` (docker/linux/container) in both tiers.

---

## Smoke tests

### T1: full_workflow (matrix — base + full)

*Unchanged from current implementation.*

`new` → wait for sentinel → `diff` → `apply` (assert content) → `log` → `sandbox info`.

### T2: stop_start (matrix — base + full)

*Strengthened from current implementation; promoted to a matrix test.*

`new --prompt "echo smoke > output.txt && touch <exdir>/done"` → wait for sentinel →
`restart --prompt "echo restarted > output2.txt && touch <exdir>/done2"` → wait for
sentinel2 → `diff` → assert `output2.txt` in diff → `apply` → assert `output2.txt`
exists in project dir with content `"restarted"`.

The prompt must write to the work copy (not just the exchange dir). Without this, `diff`
shows nothing and the diff/apply assertion is vacuous.

Tests credential re-injection AND full workflow correctness after a container restart.
Runs across the backend matrix because both are per-backend concerns.

**Prompt fragility:** The prompt assumes the agent executes shell commands verbatim.
Claude Code wraps commands in tool calls and may add preamble, confirmation prompts, or
refuse commands it interprets as destructive. If agent behavior changes between versions,
the sentinel may never appear, making T2 flaky. Mitigations: (1) the prompt is kept
minimal and imperative to maximize compliance, (2) if flakiness becomes a problem, T2
could switch to `--agent shell` for deterministic restart testing while T1 retains the
real-agent coverage, (3) as a fallback assertion, check whether the agent wrote *anything*
to the work copy (weaker but more resilient than checking for a specific file).

The diff/apply step after restart is load-bearing. The `recreateContainer` code path
(used by `restart`) must call `executeVMWorkDirSetup` on VM backends to re-establish
the git baseline in VM-local storage. Without it, the agent can still write files
(VirtioFS works) so the sentinel appears and the test looks green — but diff/apply
will fail because the baseline is absent. The original `stop_start` test only checked
the sentinel and would have missed this (and did: see commit ee314b8).

### T4: isolation_check (container backends only — base + full)

*New.*

`new --network-isolated` → start → wait for active → exec `curl -sf --max-time 5 https://1.1.1.1` →
assert non-zero exit. Then exec `curl -sf --max-time 5 http://127.0.0.1` → assert exit is not
28 (loopback is not blocked by our rules; connection refused (7) is the expected result since
nothing listens on port 80).

Verifies that iptables rules applied by `entrypoint.py` are actually in effect, not just configured.
Requires `NET_ADMIN` cap, which the sandbox layer adds automatically for isolated sandboxes.

**Scoped to container backends only** (Docker, Podman, containerd-vm). The iptables rules are
applied by `entrypoint.py` inside the container — identical code regardless of the OCI runtime.
Running T4 on every backend adds minutes for zero additional coverage. VM backends like Tart
and Seatbelt don't use `entrypoint.py` and may implement isolation differently (or not at all);
skip them with a clear message rather than producing a misleading result.

**Relationship to `TestIntegration_NetworkIsolation`:** The Go integration test
(`sandbox/integration_test.go`) already covers the same assertions (curl to 1.1.1.1,
loopback check, runtime-config.json verification) against Docker. T4 in the smoke tier
adds value by running the check across additional container backends (Podman,
containerd-vm) and validating that isolation works in the full `new` → agent startup
flow, not just the programmatic sandbox API. If only Docker needs coverage, the
integration test is sufficient and T4 can be deferred.

### T3: clone (matrix — full only)

`new A` → wait for sentinel → `clone A B` → `diff B` → assert agent-written file appears.

Kept in smoke (rather than moved fully to integration) because it specifically proves that
agent-written changes — not just mechanically-seeded work-copy state — survive a clone.
The integration test (`TestIntegration_Clone`) covers the mechanics; this covers the
agent + clone combination. Full tier only; runs across matrix backends.

**Known gap:** `TestIntegration_Clone` only exercises Docker. On VM backends the baseline
lives in VM-local storage and is re-established by `executeVMWorkDirSetup`. A regression
in the VM clone path would only be caught by the smoke T3 (full tier), not by CI.
Consider adding `TestIntegration_Clone` to `sandbox/integration_tart_test.go` to get
mechanical coverage of that path without requiring a real agent.

---

## New Go integration tests

### TestCLI_StartAfterDone (`internal/cli/integration_test.go`)

Regression test for the tmux fixed-socket-path bug (baef847). `start` (no prompt) must
succeed when the sandbox is in `StatusDone`.

1. `new --agent shell --prompt "sleep 5" <name> <project>` — shell agent exits after
   sleep, sandbox reaches `StatusDone`. Use a prompt that exits naturally without a
   sentinel file (the test cares about the done state, not agent output).
2. Poll via `testutil.WaitForStatus` until status = `done` (timeout 30s).
3. `start <name>` (no `--prompt`) → assert exit 0.

The test uses `--agent shell` because it exits via `PromptModeHeadless` and reliably
reaches `StatusDone`. Claude's interactive mode leaves status stuck at `idle`.

Add `testutil.WaitForStatus(ctx, t, statusFn func(context.Context) (string, error), want string, timeout)` to
`internal/testutil/wait.go` alongside the existing `WaitForActive`. Usage:

```go
testutil.WaitForStatus(ctx, t, func(ctx context.Context) (string, error) {
    s, err := sandbox.DetectStatus(ctx, rt, sandbox.InstanceName(name), sandbox.Dir(name))
    return string(s), err
}, string(sandbox.StatusDone), 30*time.Second)
```

### TestCLI_FilesExchange (`internal/cli/integration_test.go`)

Round-trip test for `files put / ls / get`.

1. `new --no-start <name> <project>`
2. Write `somefile.txt` to a temp path
3. `files <name> put <path>` → assert exit 0
4. `files <name> ls` → assert `somefile.txt` in output
5. `files <name> get somefile.txt -o <outdir>` → assert file exists with correct content

### TestCLI_Apply (`internal/cli/integration_test.go`)

End-to-end test for `apply` through the CLI. The sandbox integration test
(`TestIntegration_ApplyPatch`) goes through the sandbox layer directly; this exercises
the CLI argument parsing and `--yes` flag path.

1. `new --no-start <name> <project>`
2. Manually write a changed file into the sandbox work copy (same pattern as
   `TestCLI_Diff`)
3. `apply <name> --yes` → assert exit 0
4. Assert `main.go` in the original project dir contains the expected modification string
   (e.g. `"apply-test"` — use a distinctive value to prevent false positives)

### TestIntegration_Clone (`sandbox/integration_test.go`)

Tests that `clone` captures work-copy state including changes, not just the baseline.

1. Create sandbox A with `--no-start`
2. Write a changed file directly into A's work copy
3. `manager.Clone(ctx, A, B)`
4. `manager.Diff(ctx, B)` → assert changed file appears in diff

### TestIntegration_Overlay (`sandbox/integration_test.go`)

Tests the `:overlay` workdir mode end-to-end.

The overlayfs `mount` call runs **inside the container** (via `entrypoint.py`), not on
the host. Docker grants CAP_SYS_ADMIN to containers regardless of host euid — any user
in the `docker` group can create containers with elevated capabilities. Therefore, do NOT
use `os.Geteuid() != 0` as a skip guard (it would skip on CI and most dev machines).

Instead, attempt the overlay creation and skip on failure. The test should catch the
`Create` error and call `t.Skip("overlay not supported: ...")` if it indicates a
capability or mount failure. This handles rootless Docker/Podman (where user-namespace
CAP_SYS_ADMIN may be insufficient for real overlayfs) without false skips on standard
Docker.

1. Create sandbox with overlay workdir (`<project>:overlay`)
2. Start container; exec a write command inside
3. `manager.Diff` → assert changed file appears
4. `manager.Apply` → assert change lands in project dir

---

## `--full` flag implementation

```python
parser.add_argument("--full", action="store_true",
    help="Run the full test suite and all backend matrix entries. "
         "Aborts if any configured backend is unavailable.")
```

- Without `--full` (default): BASE_*_BACKENDS matrix; T1 + T2 + T4 only.
  Unavailable backends are skipped with a warning (current `--limited` behavior).
- With `--full`: FULL_*_BACKENDS matrix; T1 + T2 + T3 + T4. Aborts if any configured
  backend is unavailable.

The `--limited` flag is removed. Its skip-on-unavailable behavior becomes the default.
This eliminates the three-way confusion between `--full`, `--limited`, and bare invocation.

**Breaking change:** Bare invocation of `smoke_test.py` (without `make`) previously ran
the full matrix and aborted on missing backends. After this change, bare invocation runs
the base tier with skip behavior. Scripts that need the full matrix must add `--full`.
Document in `docs/BREAKING-CHANGES.md`.

```python
FULL_ONLY_TESTS = {"clone"}

def is_full_test(name: str) -> bool:
    base = name.split("/")[0]
    return base in FULL_ONLY_TESTS
```

Both tiers use `--debug`. It only affects log verbosity, not test behavior, and verbose
output is most valuable for the full pre-release run where no operator is nearby.

Each test gets its own project directory via `t.project(label)` (a temp dir with a seed
`main.go`). Tests may use generic filenames like `output.txt` without collision risk
because project directories are never shared between tests.

---

## Go integration test conventions

**Parallelism:** New tests do not use `t.Parallel()`, consistent with the existing
`sandbox/integration_test.go` and `internal/cli/integration_test.go` suites. These tests
share Docker state and isolated HOME directories; parallelism would require unique sandbox
names per test and careful cleanup ordering.

**Smoke test cleanup:** The smoke test uses `atexit.register(cleanup, ctx)` which destroys
all sandboxes registered via `t.sandbox(label)` (appends to `ctx.sandboxes`) — including
sandboxes from new tests — regardless of whether the test passed or failed. No new
cleanup code is needed for T4.

---

## CI integration

### GitHub Actions capabilities

| Tier | GitHub-hosted? | Notes |
|------|---------------|-------|
| `make check` + `make integration` | ✓ already in CI | PR gate; no API key needed |
| Nightly Docker smoke | ✓ feasible | `ubuntu-latest`; needs `ANTHROPIC_API_KEY` secret |
| `smoketest-full` (full matrix) | ✗ | Self-hosted only — QEMU and Tart need bare metal |

Standard `ubuntu-latest` runners run in VMs. They support Docker (including `NET_ADMIN`
for network isolation tests) but do not expose `/dev/kvm`, so containerd-vm (QEMU) and
nested-VM backends cannot run. macOS runners are VMs — Tart cannot run VMs inside them.

### Nightly smoke job

Add to `ci.yml`:

```yaml
on:
  # existing triggers ...
  schedule:
    - cron: '0 3 * * *'   # nightly at 03:00 UTC

jobs:
  # existing jobs ...

  smoke-docker:
    runs-on: ubuntu-latest
    needs: integration
    if: github.event_name == 'schedule' || (github.event_name == 'push' && github.ref == 'refs/heads/main')
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - name: Build binary and base image
        run: make build base-image
      - name: Run smoke tests (Docker)
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
        run: make smoketest
```

Without `--full`, unavailable backends (containerd-vm) are skipped automatically —
no extra scoping is needed. The job exercises T1 (`full_workflow`), T2 (`stop_start`),
and T4 (`isolation_check`) against Docker. This gives a "real Claude worked end-to-end"
signal in CI without requiring self-hosted infrastructure.

### Nightly audit job

A separate `nightly-audit` job (schedule-only) runs `govulncheck`, `hadolint`, and
`actionlint`. These catch vulnerability disclosures and Dockerfile drift between PRs.
No API key needed.

### Nightly failure alerting

Unmonitored nightly jobs are write-only infrastructure. GitHub Actions sends email
notifications to repo watchers on workflow failure by default. Verify this is enabled
(Settings → Notifications → "Send notifications for failed workflows only"). For
additional alerting (e.g., Slack), add a final job with `if: failure()` that posts to a
webhook:

```yaml
notify-failure:
  needs: [smoke-docker, nightly-audit]
  if: failure()
  runs-on: ubuntu-latest
  steps:
    - name: Notify
      run: curl -X POST "${{ secrets.SLACK_WEBHOOK }}" ...
```

The nightly smoke job and nightly audit job should both trigger alerts.

API key expiry is a common silent failure mode. If `ANTHROPIC_API_KEY` expires, the
nightly smoke job will fail with an auth error. The failure alert covers this, but
consider adding a comment in the workflow file noting the key's expected rotation
cadence so maintainers know to check it.

### Pre-release full tier

`smoketest-full` runs manually on:
- A self-hosted Linux machine with QEMU/KVM for containerd-vm and containerd-vmenhanced.
- A self-hosted macOS Apple Silicon machine for Tart.

These are not automatable on GitHub-hosted runners and are intentionally pre-release only.

---

## Tier ownership

Each test tier answers a different question. This table is the definitive guide to
which tier a new test belongs in.

| Tier | Question answered | Real agent? | Real container? | CI? |
|------|-------------------|-------------|-----------------|-----|
| Unit (`go test ./...`) | Does the logic work? | No | No | PR gate |
| Integration (`make integration`) | Does lifecycle work with Docker? | No (stub agent) | Yes | PR gate |
| E2E (`make e2e`) | Does the binary start, parse args, and exit cleanly? | No | Yes | PR gate |
| Smoke base (`make smoketest`) | Does a real agent work end-to-end on Docker? | Yes | Yes | Nightly |
| Smoke full (`make smoketest-full`) | Does the full backend matrix pass? | Yes | Yes | Pre-release |

The **e2e tier** validates binary-level concerns (exit codes, error messages, `--json` output
format). It is not a workflow test — it does not modify work copies or exercise diff/apply
with real content. The smoke tier owns that question.

---

## JUnit XML output

Add `--junit <path>` flag to `smoke_test.py`:

```python
parser.add_argument("--junit", metavar="PATH",
    help="Write JUnit XML test report to PATH")
```

Hand-roll the XML (the format is simple; no third-party dependency needed):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="smoke" tests="N" failures="F" skipped="S" time="T">
    <testcase name="full_workflow/docker" time="12.3"/>
    <testcase name="isolation_check/docker" time="4.1"/>
    <testcase name="clone/docker">
      <skipped message="full tier only"/>
    </testcase>
    <testcase name="full_workflow/containerd-vm">
      <failure message="sentinel not seen in 300s"/>
    </testcase>
  </testsuite>
</testsuites>
```

The nightly CI job writes the XML and uploads it:

```yaml
- name: Run smoke tests (Docker)
  env:
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
  run: make smoketest SMOKE_ARGS='--junit smoke-results.xml'

- uses: actions/upload-artifact@v4
  if: always()
  with:
    name: smoke-test-results
    path: smoke-results.xml
```

This gives historical tracking via GitHub Actions artifacts without any external service.

**Crash resilience:** Write the XML incrementally — open the file and write the
`<testsuites><testsuite>` header at the start of the run, append `<testcase>` elements
as each test completes, and write the closing tags at the end. If the process is killed
mid-run (e.g., OOM on a CI runner), the partial file is still parseable by most JUnit
consumers. Use `atexit.register` to attempt writing closing tags on abnormal exit.

---

## Known gaps

### CAP_SYS_ADMIN containment (overlay mode)

`TestIntegration_Overlay` tests that overlay diff/apply works, but `CAP_SYS_ADMIN` is a
broad capability that permits namespace manipulation, mount operations, and more. There is
no test that verifies the sandbox doesn't *leak* this capability in a way that allows
container escape. A full container escape test is out of scope for this plan, but the
tradeoff is documented in `docs/design/security.md` (line 106). The mitigation is that
the container's namespace isolation limits the blast radius, and `:copy` mode avoids the
capability entirely.

### Concurrent sandbox creation (known crash path)

Two concurrent `yoloai new` with the same name corrupts CNI networking on containerd
backends — `netns.NewNamed` switches the OS thread and the pre-flight DEL deletes live
rules from the first sandbox (documented in `docs/dev/backend-idiosyncrasies.md`). This
is not a speculative race condition; it's a known bug with a code-level workaround but no
test verifying the workaround holds.

Beyond the same-name case, no test runs sandbox operations concurrently at all (e.g.,
creating two different sandboxes simultaneously, or diff on one while apply runs on
another). Race conditions in shared state — Docker daemon API, sandbox directory listing,
file locks — would only be caught by concurrent testing.

A test for the specific same-name concurrent creation case should be prioritized for
multi-user/CI environments. A broader concurrent test (`t.Run` two goroutines that each
create/diff/destroy separate sandboxes) would catch lock contention and shared-state races
without complex orchestration. Consider also adding a file lock guard on sandbox name in
the creation path as a defense-in-depth measure.

### Status monitor accuracy

The status monitor (`runtime/monitor/status-monitor.py`) determines agent idle/active
state. It has 5 detector implementations (hook, wchan, ready_pattern, context_signal,
output_stability), stability counting logic (MEDIUM_STABILITY, LOW_STABILITY,
GLOBAL_HOLD_CYCLES), grace periods, and priority ordering. None of it is tested — no unit
tests, no integration tests.

A false-positive idle detection could signal turn completion while the agent is still
working. A false-negative could leave the sandbox stuck in "running" state indefinitely.
The smoke tests implicitly depend on correct status transitions (sentinel wait uses status
to detect stalls), but no test verifies the monitor itself.

High-value first test: `TestMonitor_HookDetector` — feed scripted hook events (touch
idle/active marker files), verify the monitor produces correct status JSON transitions.
This can run without a real agent.

### Cleanup-on-error paths

`sandbox/create.go` silently ignores errors during failure cleanup:
```go
_ = os.RemoveAll(state.sandboxDir)
_ = m.runtime.Remove(ctx, InstanceName(state.name))
```

If `runtime.Remove()` fails (e.g., orphaned Kata shim with EADDRINUSE — documented in
`backend-idiosyncrasies.md`), the container keeps running while the sandbox directory is
deleted. This creates orphaned resources that `system prune` must clean up, but the user
gets no indication.

A focused test: create a sandbox, manually remove its sandbox directory, then verify
`system prune` finds and removes the orphaned container. This tests the safety net without
needing to simulate a crash.

### Backend workaround regression coverage

`docs/dev/backend-idiosyncrasies.md` catalogs 10+ workarounds (Kata stale sockets,
containerd snapshot GC, Tart VirtioFS corruption, gVisor permissions, Docker exec newline
stripping). Each has a code-level fix but no test verifying the fix prevents the bad state.
If a refactor removes a workaround, nothing catches the regression.

Most impactful subset to test first: (1) Kata stale socket recovery (pre-flight cleanup
in containerd backend), (2) containerd snapshot orphan recovery, (3) gVisor permission
detection (standard gets 0750/0600, gVisor gets 0777/0666 — see next entry).

### gVisor permission mode detection

`docs/design/security.md` documents that gVisor gets relaxed permissions (0777/0666) and
standard Docker gets restrictive ones (0750/0600). The detection and permission-setting
logic is in production code but has no test. A regression here is silent — files would be
world-readable on standard Docker or inaccessible on gVisor.

Test: create a standard sandbox, verify work dir permissions are 0750. The gVisor path
can't run on CI (not available on standard runners) but the standard-mode permission
assertion can.

---

## Confidence statement

What this test suite verifies when fully implemented:

- **Happy-path lifecycle on Docker** (integration tier, PR gate): create, start, exec,
  diff, apply, stop, restart, destroy, clone, overlay, credential injection, read-only
  mount enforcement, network isolation.
- **End-to-end agent workflow on docker + primary VM** (smoke base, nightly): real agent
  produces output, diff/apply captures it, restart re-injects credentials and preserves
  work-copy baseline, network isolation enforced on container backends.
- **Full backend matrix** (smoke full, pre-release): all of the above across Docker,
  Podman, gVisor, containerd-vm (QEMU), containerd-vmenhanced, Seatbelt, and Tart.

What it does **not** verify:

- **Failure recovery** — crash during create, orphaned resources after abnormal exit,
  cleanup-on-error paths.
- **Concurrent operations** — race conditions in creation, diff, apply with simultaneous
  sandboxes.
- **Status monitor accuracy** — no test for detector logic, stability counting, or grace
  period timing.
- **Backend workaround stability** — Kata socket recovery, containerd snapshot GC, Tart
  VirtioFS workarounds are untested.
- **gVisor permission modes** — standard vs gVisor permission detection is untested.
- **Agent behavior drift** — smoke tests assume Claude executes prompts verbatim; behavior
  changes across agent versions could cause flakiness.

---

## Out of scope

- `attach` — inherently interactive (tmux); not automatable.
- `sandbox bugreport` — already covered in e2e and CLI integration tests.
- `exec`, `:rw`, aux dirs, `reset`, `destroy`, `stop` standalone, `allow`/`deny` —
  covered in `sandbox/integration_test.go` and `internal/cli/integration_test.go`.
- Multi-agent (Gemini, Codex) — separate run mode gated on key presence.
- `profile` / `config` commands — admin surface; not lifecycle.

---

## Implementation status

What exists today vs what this plan specifies. Updated 2026-04-01.

### Done (in code)

- [x] `TestIntegration_NetworkIsolation` — runtime-config.json + curl assertions (`sandbox/integration_test.go`)
- [x] `TestIntegration_ReadOnlyMountVerified` — exec write to RO aux dir fails (`sandbox/integration_test.go`)
- [x] `TestIntegration_CredentialInjection` — /run/secrets lifecycle + host cleanup (`sandbox/integration_test.go`). Happy path only; failure-path cleanup (crash during create) is untested but handled by Go defers which survive panics.
- [x] `testutil.WaitForStatus` helper (`internal/testutil/wait.go`)
- [x] Nightly `smoke-docker` CI job (`.github/workflows/ci.yml`)
- [x] Nightly `nightly-audit` CI job — govulncheck + hadolint + actionlint (`.github/workflows/ci.yml`)
- [x] Schedule trigger (`cron: '0 3 * * *'`) in CI
- [x] Makefile `smoketest` target uses `--limited --debug $(SMOKE_ARGS)` (will become `--debug $(SMOKE_ARGS)` when `--full` lands)
- [x] Makefile `smoketest-full` target uses `--debug $(SMOKE_ARGS)` (will become `--full --debug $(SMOKE_ARGS)` when `--full` lands)

### Pending (design only)

- [ ] Replace `--limited` with `--full` flag in `smoke_test.py`
- [ ] Split `LINUX_BACKENDS` / `MACOS_BACKENDS` into `BASE_*` / `FULL_*` constants
- [ ] Add `FULL_ONLY_TESTS` set and `is_full_test()` gate
- [ ] T2 (`stop_start`): update prompt to write to work copy + add diff/apply assertion
- [ ] T4 (`isolation_check`): new smoke test function
- [ ] T3 (`clone`): restrict to full tier only
- [ ] Remove smoke tests moved to integration tier: `start_done_agent`, `files_exchange`, `overlay`, `reset`
- [ ] `TestCLI_StartAfterDone` (`internal/cli/integration_test.go`)
- [ ] `TestCLI_FilesExchange` (`internal/cli/integration_test.go`)
- [ ] `TestCLI_Apply` (`internal/cli/integration_test.go`)
- [ ] `TestIntegration_Clone` (`sandbox/integration_test.go`)
- [ ] `TestIntegration_Overlay` (`sandbox/integration_test.go`)
- [ ] `--junit <path>` flag with incremental XML output
- [ ] JUnit artifact upload in CI smoke job
- [ ] Nightly failure alerting verification (GitHub notification settings)
- [ ] Breaking change entry in `docs/BREAKING-CHANGES.md` for `--limited` removal

### Future (from known gaps — lower priority)

- [ ] `TestMonitor_HookDetector` — status monitor unit test: feed scripted hook events, verify status JSON transitions
- [ ] Standard-mode permission assertion — verify work dir gets 0750 on non-gVisor Docker
- [ ] Orphan detection via `system prune` — create sandbox, remove its directory, verify prune finds the container
- [ ] Concurrent same-name creation test — verify two parallel `new` with same name doesn't corrupt CNI state
- [ ] Kata stale socket recovery test — verify pre-flight cleanup handles `/run/kata/<name>` leftover
- [ ] Containerd snapshot orphan recovery test — verify snapshot GC after abnormal exit
