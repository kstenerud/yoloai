# Critique

(Empty - ready for next critique cycle)

## Previous Critique (2026-03-31): Smoke Test V2 — Resolved

All issues from the smoke-test-v2 critique have been addressed:

- ✅ Security boundary test added (`TestIntegration_NetworkIsolation` + smoke T4 `isolation_check`)
- ✅ `WaitForStatus` uses callback signature — no import cycle
- ✅ `--debug` kept for both tiers (log verbosity only, no behavior risk)
- ✅ Base tier timing expectation updated to 15 min (was 5 min; `stop_start` on QEMU is slow)
- ✅ CI story documented: nightly `smoke-docker` job on GitHub-hosted runners; full tier is self-hosted only
- ✅ `unix.Prctl` → `os.Geteuid() != 0` for capability check in `TestIntegration_Overlay`
- ✅ Clone-on-VM-backends gap noted in plan
- ✅ `--limited`/`--full` migration flagged as breaking change
- ✅ `TestCLI_Apply` step 4 now specifies a distinctive assertion string
- ✅ `t.Parallel()` policy stated (not used, consistent with existing suite)
- ✅ Smoke cleanup handled by existing `atexit.register(cleanup, ctx)` — noted in plan
