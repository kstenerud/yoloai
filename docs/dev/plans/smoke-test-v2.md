# Smoke Test V2: Base / Full Split

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

Also add `testutil.WaitForStatus` helper to support `TestCLI_StartAfterDone`.

### Smoke test (`smoke_test.py`)

- Add `--full` flag to select backend matrix width and test depth
- Remove: `start_done_agent`, `files_exchange`, `clone`, `reset`, `overlay` (moved down)
- Base tier: `full_workflow` (docker + one VM) + `stop_start`
- Full tier: `full_workflow` on full matrix + `stop_start` on full matrix + `clone`
  (kept in full because it confirms agent-written changes survive a clone, not just
  mechanical clone behavior)

### Makefile

```makefile
smoketest: build
    python3 scripts/smoke_test.py --limited --debug

smoketest-full: build
    python3 scripts/smoke_test.py --full
```

---

## Tier definitions

### Base (`smoketest`)

Runs with `--limited --debug`. Skips unavailable backends rather than aborting.
Intended for developer local runs and PR CI.

**Backend matrix** — docker + primary VM per platform:
- Linux: docker, containerd-vm
- macOS: docker, tart

**Tests**: `full_workflow` and `stop_start` on each matrix backend.

Target wall-clock time: under 5 minutes on a warm machine.

### Full (`smoketest-full`)

Runs without `--limited`. Aborts if any configured backend is unavailable.
Intended for pre-release runs on the dedicated test machine.

**Backend matrix** — all backends per platform:
- Linux: docker, podman, docker-cenhanced, containerd-vm, containerd-vmenhanced
- macOS: docker, podman, seatbelt, tart

**Tests**: `full_workflow`, `stop_start`, and `clone` on the full matrix.

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

`new` → wait for sentinel → `restart --prompt <sentinel2>` → wait for sentinel2 →
`diff` → `apply` → assert applied content.

Tests credential re-injection AND full workflow correctness after a container restart.
Runs across the backend matrix because both are per-backend concerns.

The diff/apply step after restart is load-bearing. The `recreateContainer` code path
(used by `restart`) must call `executeVMWorkDirSetup` on VM backends to re-establish
the git baseline in VM-local storage. Without it, the agent can still write files
(VirtioFS works) so the sentinel appears and the test looks green — but diff/apply
will fail because the baseline is absent. The original `stop_start` test only checked
the sentinel and would have missed this (and did: see commit ee314b8).

### T3: clone (matrix — full only)

`new A` → wait for sentinel → `clone A B` → `diff B` → assert agent-written file appears.

Kept in smoke (rather than moved fully to integration) because it specifically proves that
agent-written changes — not just mechanically-seeded work-copy state — survive a clone.
The integration test (`TestIntegration_Clone`) covers the mechanics; this covers the
agent + clone combination. Full tier only; runs across matrix backends.

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

Add `testutil.WaitForStatus(ctx, t, rt, instance, status string, timeout)` to
`internal/testutil/wait.go` alongside the existing `WaitForActive`.

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
4. Assert the change exists in the original project dir

### TestIntegration_Clone (`sandbox/integration_test.go`)

Tests that `clone` captures work-copy state including changes, not just the baseline.

1. Create sandbox A with `--no-start`
2. Write a changed file directly into A's work copy
3. `manager.Clone(ctx, A, B)`
4. `manager.Diff(ctx, B)` → assert changed file appears in diff

### TestIntegration_Overlay (`sandbox/integration_test.go`)

Tests the `:overlay` workdir mode end-to-end.

Skip if `CAP_SYS_ADMIN` is not available (use `unix.Prctl` or check capabilities; print
a clear skip message rather than failing).

1. Create sandbox with overlay workdir (`<project>:overlay`)
2. Start container; exec a write command inside
3. `manager.Diff` → assert changed file appears
4. `manager.Apply` → assert change lands in project dir

---

## `--full` flag implementation

```python
parser.add_argument("--full", action="store_true",
    help="Run the full test suite and all backend matrix entries.")
```

- Without `--full`: BASE_*_BACKENDS matrix; T1 + T2 only.
- With `--full`: FULL_*_BACKENDS matrix; T1 + T2 + T3.
- `--full` and `--limited` are mutually exclusive; check at startup with a clear error.

```python
FULL_ONLY_TESTS = {"clone"}

def is_full_test(name: str) -> bool:
    base = name.split("/")[0]
    return base in FULL_ONLY_TESTS
```

`smoketest-full` drops `--debug` (bugreport overhead unnecessary for release validation).

---

## Out of scope

- `attach` — inherently interactive (tmux); not automatable.
- `sandbox bugreport` — already covered in e2e and CLI integration tests.
- `exec`, `:rw`, aux dirs, `reset`, `destroy`, `stop` standalone, `allow`/`deny` —
  covered in `sandbox/integration_test.go` and `internal/cli/integration_test.go`.
- Multi-agent (Gemini, Codex) — separate run mode gated on key presence.
- `profile` / `config` commands — admin surface; not lifecycle.
