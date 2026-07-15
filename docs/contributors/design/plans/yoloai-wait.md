> **ABOUTME:** Add `yoloai wait` to block until a named sandbox's agent exits and return its
> exit code, so CI/CD scripting doesn't have to poll `yoloai list --json`.

# `yoloai wait`

- **Status:** UNSPECIFIED — idea only; shape sketched below, not designed.
- **Depends on:** —

Block until the agent in a named sandbox exits, then return the agent's exit code. Useful for CI/CD pipelines and scripting. Without `wait`, polling `yoloai list --json` is the only way to detect completion.

```
yoloai wait <name> [--timeout <duration>]
```

- Blocks until the sandbox's tmux pane is dead (agent has exited)
- Returns the agent's exit code as yoloai's exit code (0 = done, non-zero = failed)
- `--timeout`: fail with exit code 124 (matching `timeout(1)`) if the agent hasn't exited within the duration
- Related to the deferred `yoloai run` (#56 in OPEN_QUESTIONS) — `run` would be sugar on top of `wait`

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §77.
