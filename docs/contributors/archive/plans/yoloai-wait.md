> **ABOUTME:** Add `yoloai wait` to block until a named sandbox's agent exits and return its
> exit code, so CI/CD scripting doesn't have to poll `yoloai list --json`.

# `yoloai wait`

> **IMPLEMENTED 2026-07-18.** `yoloai wait` ships with `--for idle|exit` and `--timeout`. This
> cycle added the load-bearing half the sketch below asked for: `--for exit` now propagates the
> agent's own process exit code as yoloai's (0 when the agent finished cleanly, the agent's
> non-zero code when it failed), and `--timeout` exits **124** (matching `timeout(1)`). The
> numeric code is plumbed agent-status.json → `status.Info.ExitCode` → `SandboxInfo.ExitCode`
> and mapped to the process exit in `internal/cli/lifecycle/wait.go` (`waitExitCode` + `os.Exit`,
> mirroring how `yoloai exec` propagates a child command's status). Verified end-to-end on
> docker with the credential-free `test` agent (exit 3 → 3, exit 0 → 0, timeout → 124). The
> text below is the original sketch, kept for history.

- **Status:** IMPLEMENTED
- **Depends on:** —
- **Rides:** **any** release — filed as a minor behavior change, not breaking (owner's call, 2026-07-18). `wait --for exit` did change from always-0 to the agent's own code, but it is a non-default mode whose new behavior is the intended one; no flag/config/promised-capability was withdrawn.

Block until the agent in a named sandbox exits, then return the agent's exit code. Useful for CI/CD pipelines and scripting. Without `wait`, polling `yoloai list --json` is the only way to detect completion.

```
yoloai wait <name> [--timeout <duration>]
```

- Blocks until the sandbox's tmux pane is dead (agent has exited)
- Returns the agent's exit code as yoloai's exit code (0 = done, non-zero = failed)
- `--timeout`: fail with exit code 124 (matching `timeout(1)`) if the agent hasn't exited within the duration
- Related to the deferred `yoloai run` (#56 in OPEN_QUESTIONS) — `run` would be sugar on top of `wait`

See [OPEN_QUESTIONS.md](../../design/questions-unresolved.md) §77.
