# Mac handoff — branch `opaque-error-diagnostics`

**Transient.** Delete this file in the PR that finishes the last task below. It exists because three
backends (apple, tart, seatbelt) can only be *verified* on macOS, and the Linux half of this branch
is done. Read `AGENTS.md` first; it is the contract. Everything here points at durable records that
own the detail — trust them over this summary if they disagree.

## What already landed on this branch (Linux, verified, do not redo)

- **DF145 Phase 1** — opaque subprocess errors now surface their stderr. A shared helper
  `sysexec.EnrichExitError` (`internal/sysexec/exiterr.go`) plus fixes to `runtime/podman/podman.go`
  and `internal/orchestrator/files.go`. Green.
- **Speedup levers 1–2** — `runtime/runtimetest/conformance_iface.go` rewritten: per-backend fixture
  policy (`SharesReadOnlyInstance`, `MaxConcurrentInstances`), an `instanceGate` sized from tart's
  live `VMCensus` free slots, read-only subtests share one instance on sharing backends. Verified on
  docker (6.2s→3.0s) and by a fake-backend boot-count test (`conformance_share_test.go`). Plan:
  `docs/contributors/design/plans/integration-test-speedup.md` (Status IN-PROGRESS — read it).
- A `test(status)` regression test, and docs (DF145, DF146, A11, next-release staging).

## Task A — Speedup Mac-check phase (the reason for the trip)

The plan's "Phasing" and "Open questions" sections are authoritative. Concretely:

1. **Turn the sharing lever on for the VM backends.** In `runtime/tart/integration_test.go` and
   `runtime/apple/integration_test.go`, set `SharesReadOnlyInstance: true` on the `InterfaceBackend`
   the conformance setup returns. Nothing else needs changing — the field is wired.
2. **Run and measure.** `YOLOAI_TEST_TART_VM=1 go test -tags=integration -run TestTartConformance
   ./runtime/tart/` and the apple equivalent. Confirm green and record before/after wall-clock in the
   plan (the whole point is the minutes saved).
3. **Answer the open question:** does tart do **reliable concurrent `Exec`** against one running VM?
   The suite currently runs sharing backends *serially* on purpose. If concurrent exec is reliable,
   the shared read-only subtests can be parallelised (bounded by the free-slot gate) for more savings;
   if not, leave them serial. Decide it empirically on hardware and record it in the plan.
4. **Levers 3–5** (see the plan): trim redundant slow-backend smoke coverage; consider consolidating
   `internal/orchestrator/integration_tart_test.go`'s five VM boots; and un-gate the VM-free
   tart/seatbelt tests (they need a `if !isMacOS() { t.Skip }` guard — untagging alone would fail
   `make check` on Linux, which is why it is a Mac-side item).

## Task B — DF145 Phase 2 (the macOS error-forwarding fixes)

`docs/contributors/design/findings-unresolved.md` DF145 has the full pointer list. The helper to reuse
(`sysexec.EnrichExitError`) is already committed. Two shapes:

- **Streaming image/VM builds** → mirror the DF144 `TailBuffer` remedy (see `runtime/docker/build.go`
  lines ~178-187): `runtime/apple/apple.go:455` (the exact DF144 case), `runtime/tart/build.go`
  (:444, :351, :371, :363).
- **`.Output()` calls that drop `ExitError.Stderr`** → wrap with a context noun and route through
  `sysexec.EnrichExitError`: `runtime/tart/runtime.go:66`, `internal/envsetup/keychain_darwin.go:26`,
  `runtime/tart/build.go:244`.

Verify on hardware where you can; unit-test the pure logic. When the mac half lands, move DF145 from
`findings-unresolved.md` to `findings-resolved.md` and repoint the `next-release.md` link.

## Task C — DF146 lint cleanup (the lint issues you asked to include)

`golangci-lint` runs `./...` with **no build tags** everywhere (make check, `make lint`,
`make lint-cross`, the CI action), so `//go:build integration` files are never linted. They have
**32 accumulated issues**. Full detail and the trap are in DF146 (`findings-unresolved.md`).

To see them: `golangci-lint run --build-tags integration ./...`. On macOS this also covers the darwin
files (apple/tart/seatbelt) that Linux cannot compile — so the Mac is the right place to close it out.

- **Genuine, fix them:** `runtime/containerd/integration_test.go:68` `func testSandboxDir` is unused;
  `internal/cli/integration_main_test.go:143` unchecked `os.RemoveAll`.
- **Test-edge false positives:** the `gosec` G304/G306/G301 on `t.TempDir()`-controlled paths and the
  `forbidigo` `os.WriteFile`/`GetCuratedHostEnv` at test edges want a scoped `//nolint` with a reason,
  or a `.golangci.yml` allowlist entry — not a behavioural change. `conformance_iface.go:400,407` are
  in this bucket (pre-existing Mounts-section lines, just relocated by the refactor).
- **The trap (read DF146):** 7 of the 32 are `nolintlint` "directive unused" — a `//nolint` needed
  under the *untagged* lint is flagged under the *tagged* one (or vice versa). So you must reconcile
  each so **both** `golangci-lint run ./...` **and** `... --build-tags integration ./...` pass.
  `make check` (untagged) must stay green — check it after every nolint edit.
- Whether to then wire `--build-tags integration` into CI so this never rots again is the owner's
  call; propose it if you close the issues.

## Conventions (from AGENTS.md — do not skip)

- `make check` is the local gate but **not** all of CI; it does not lint integration files (that is
  DF146). Verify tagged code with `go vet -tags integration ./...` and the real hardware runs.
- Commits: `type(scope): summary`, imperative, no trailing period; a prose body saying what was wrong
  and why the fix is right; `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`; cite `DF145` /
  `DF146` where you write rationale. Commit author `Karl Stenerud <kstenerud@gmail.com>`.
- Absent platform-possible backends **fail**, never skip (D112); the carve-out is
  `YOLOAI_TEST_UNCONTROLLED_BACKENDS`.
- When a task completes, its record says so (move the finding, update the plan Status) — and delete
  this handoff file.
