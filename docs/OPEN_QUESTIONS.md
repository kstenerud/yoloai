# Open Questions

Questions encountered during design and implementation that need resolution. Resolve each before the relevant implementation phase begins.

## Pre-Implementation (resolve before coding starts)

1. ~~**Go module path**~~ — **Resolved:** `github.com/kstenerud/yoloai`.

2. ~~**Node.js version**~~ — **Resolved:** Node.js 22 LTS via NodeSource. Claude Code's `engines` field requires `>=18.0.0`; Node 22 is well within range. Node 20 LTS reaches EOL April 2026 — Node 22 LTS (maintenance until April 2027) avoids shipping with an EOL runtime. Anthropic's devcontainer still uses Node 20 as of February 2026, but no Node 22-specific incompatibilities have been found. The native Claude Code installer (curl script) is not suitable: bundles Bun with broken proxy support (issue #14165), segfaults on Debian bookworm AMD64 (#12044), and auto-updates. npm install shows a deprecation warning but remains the only reliable path for Docker/proxy use. See RESEARCH.md "Claude Code Installation Research".

3. ~~**tini**~~ — **Resolved:** Use `docker run --init` (Docker's built-in tini). Simpler than installing in image. We control all container creation in code so the flag is always passed.

4. ~~**gosu**~~ — **Resolved:** Install from GitHub releases (static binary). Standard for Debian images.

5. ~~**Claude ready indicator**~~ — **Resolved:** Fixed 3-second delay for MVP, configurable via `config.json` `startup_delay`. Polling deferred.

6. ~~**Caret encoding scope**~~ — **Resolved:** Implement the full caret encoding spec. Trivial to implement and avoids platform-specific assumptions.

## Deferred Items Worth Reconsidering

These were deferred from MVP but might be cheap to add and valuable for dogfooding:

7. ~~**`--model` flag**~~ — **Resolved:** Include in MVP. Trivial pass-through to agent command.

8. ~~**`yoloai exec`**~~ — **Resolved:** Include in MVP. Simple `docker exec` wrapper, useful for debugging.

9. ~~**Dangerous directory detection**~~ — **Resolved:** Include in MVP. Small validation function.

10. ~~**Dirty git repo warning**~~ — **Resolved:** Include in MVP. Small git status check.

## Design Gaps (resolve before implementing the relevant component)

### Entrypoint / Container Startup

15. ~~**Entrypoint configuration passing**~~ — **Resolved:** Bind-mounted JSON config file at `/yoloai/config.json`. Entrypoint reads all configuration from it — agent command, startup delay, UID/GID, submit sequence, and later overlay mounts, iptables rules, setup commands. Single source of truth from the start; no env vars.

16. ~~**`setup` commands — execution mechanism**~~ — **Resolved:** Post-MVP. Setup commands written to a bind-mounted script, executed by entrypoint before launching agent.

17. ~~**tmux behavior when agent exits**~~ — **Resolved:** `remain-on-exit on` — container stays up after agent exits. User can still attach and see final output. Container only stops on explicit `yoloai stop`/`destroy`.

18. ~~**Context file content and delivery**~~ — **Resolved:** Post-MVP. Context file generated on host, bind-mounted read-only. Claude gets it via `--append-system-prompt`, Codex via prompt prepend.

### Diff / Apply

19. ~~**Untracked files in diff/apply**~~ — **Resolved:** `git add -A` before diffing to capture untracked files. Runs in the sandbox copy, not the user's original.

20. ~~**Multiple `:copy` directories in diff/apply**~~ — **Resolved:** Post-MVP (MVP has single workdir, no aux dirs). Show all with headers per directory. Apply all at once with single confirmation. If one fails, stop and report which failed. User can re-run with `[-- <path>...]` to apply selectively. Future: cherry-picking agent commits as a post-v1 feature.

21. ~~**Overlay apply — patch transfer to host**~~ — **Resolved:** Capture `git diff` output from `docker exec` stdout, pipe to `git apply` on host. No temp file needed.

### Agent Files

22. ~~**`agent_files: home` — scope**~~ — **Resolved:** Post-MVP. Copy entire agent state directory excluding session history and caches. If directory doesn't exist, skip silently. Runtime state tracked in `state.json` (alongside `meta.json`).

23. ~~**`agent_files` — "first run" detection**~~ — **Resolved:** Post-MVP. Initialization detected by presence of `state.json` in agent-state directory. No `state.json` = not initialized. To re-seed, delete `state.json`.

### Build / Resources

24. ~~**How does the binary find Dockerfile and entrypoint at runtime?**~~ — **Resolved:** `go:embed` bundles defaults. On first run, seed `~/.yoloai/Dockerfile.base` and `entrypoint.sh` if they don't exist. Build always reads from `~/.yoloai/`, not embedded copies. User can edit for fast iteration.

25. ~~**Codex binary download URL and versioning**~~ — **Resolved:** Post-MVP (Codex deferred). Pin version in Dockerfile when implemented.

26. ~~**`yoloai build --secret` — which secrets are automatically provided?**~~ — **Resolved:** Post-MVP. Auto-provide `~/.npmrc` if it exists. No other automatic secrets. Additional secrets via `--secret` flag.

### Network Isolation

27. ~~**Docker network naming and lifecycle**~~ — **Resolved:** Post-MVP. Per-sandbox network: `yoloai-<name>-net`. Created during `yoloai new --network-isolated`, destroyed during `yoloai destroy`.

28. ~~**Proxy allowlist file format**~~ — **Resolved:** Post-MVP. One domain per line, `#` comments. Bind-mounted file in proxy container.

29. ~~**Proxy Go source location**~~ — **Resolved:** Post-MVP. `cmd/proxy/main.go` — separate binary in its own container, belongs in `cmd/`.

### Lifecycle Edge Cases

30. ~~**`yoloai start` when container was removed**~~ — **Resolved:** Re-run full container creation logic from `meta.json`, skip copy step. Credential injection: create new temp file each time (ephemeral by design).

31. ~~**`yoloai list` STATUS "exited" detection**~~ — **Resolved:** `docker exec tmux list-panes -t main -F '#{pane_dead}'`. Combined with Docker container state gives: running, exited, stopped, removed.

### Miscellaneous

32. ~~**Dangerous directory list**~~ — **Resolved:** `$HOME`, `/`, plus platform-specific: macOS (`/System`, `/Library`, `/Applications`), Linux (`/usr`, `/etc`, `/var`, `/boot`, `/bin`, `/sbin`, `/lib`). Simple string match on absolute path — no subdirectory blocking.

33. ~~**`yoloai diff` for `:rw` — why docker exec?**~~ — **Resolved:** `:rw` runs `git diff` directly on host (bind mount = same files). Docker exec only needed for overlay.

34. ~~**No workdir and no profile**~~ — **Resolved:** Error: "no workdir specified and no default workdir in profile" (exit 2). Workdir required for MVP.

35. ~~**`auto_commit_interval` implementation**~~ — **Resolved:** Post-MVP. Shell script loop spawned by entrypoint. `git add -A && git commit` with author `yoloai <yoloai@localhost>`, UTC timestamp message. Skips if no changes. Creates commit history for future cherry-pick feature.

36. ~~**Profile without a Dockerfile**~~ — **Resolved:** Profile creation always seeds a Dockerfile — if profile doesn't provide one, copy from base. Every profile has an explicit Dockerfile. Binary updates don't silently change behavior on existing profiles.

## UX Issues (from workflow simulation)

41. ~~**`.:copy` boilerplate**~~ — **Resolved:** Workdir defaults to `:copy` (the tool's core philosophy). `yoloai new fix-bug .` works. `:rw` requires explicit suffix. Safe default preserved.

42. ~~**Implicit workdir from cwd**~~ — **Resolved (firm decision — do not revisit):** Keep workdir explicit (`.` required). One character is low friction and avoids accidental sandboxing of wrong directory. This is a deliberate safety choice: implicit cwd defaulting is a footgun that leads to sandboxing the wrong directory. This has been discussed multiple times and the decision is final.

43. ~~**Sandbox name repetition**~~ — **Resolved:** Shell completion via `yoloai completion` (Cobra built-in) in MVP. `YOLOAI_SANDBOX` env var as fallback when name arg is omitted — explicit arg always wins. No special `yoloai use` command; users just `export YOLOAI_SANDBOX=fix-bug`.

44. ~~**No `--prompt-file` or stdin**~~ — **Resolved:** Add `--prompt-file <path>`. Both `--prompt -` and `--prompt-file -` read from stdin.

45. ~~**No reset/retry workflow**~~ — **Resolved:** Add `yoloai reset <name>` — re-copies workdir from original, resets git baseline, keeps sandbox config and agent-state.

46. ~~**First-time setup friction**~~ — **Resolved:** `yoloai new` auto-detects missing setup: creates `~/.yoloai/` if absent, builds base image if missing. `yoloai init` dropped. `yoloai new --no-start` for setup-only (create sandbox without starting container).

47. ~~**No default profile**~~ — **Resolved:** Add `defaults.profile` to config.yaml. CLI `--profile` overrides. `--no-profile` to explicitly use base image.

48. ~~**`yoloai diff` no summary mode**~~ — **Resolved:** Add `--stat` flag (passes through to `git diff --stat`).

49. ~~**`yoloai apply` all-or-nothing**~~ — **Resolved:** `yoloai apply <name> [-- <path>...]` to apply specific files only.

50. ~~**Shell completion setup**~~ — **Resolved:** `yoloai completion` command in MVP. Print setup instructions after first-run auto-setup during `yoloai new`.

51. ~~**"Is it done?" check**~~ — **Deferred.** Hard to detect agent idle vs working. `yoloai tail` and `yoloai list` are sufficient for v1.

52. ~~**Re-use prompt after destroy**~~ — **Deferred.** `yoloai reset` (#45) covers the main retry case without destroying.

## UX Issues — Round 2 (from workflow simulation)

53. ~~**No read-only/investigation mode shortcut**~~ — **Resolved:** Not a problem. `:copy` with overlay is instant. Agent needs write access even for investigation. No change needed.

54. ~~**`yoloai reset` does not re-send the prompt**~~ — **Resolved:** Reset re-sends the original `prompt.txt` by default. `--no-prompt` flag to suppress.

55. ~~**No way to send a new prompt without attaching**~~ — **Deferred.** Between reset re-sending prompt (#54) and `--prompt-file` for scripting, the gap is small. Add `yoloai prompt` command post-MVP if needed.

56. ~~**Quick successive tasks have too much ceremony**~~ — **Deferred.** `yoloai run` (create, wait, diff, prompt for apply, auto-destroy) is high-value sugar but the building blocks need to work first. Post-MVP.

57. ~~**No indication of agent completion vs. crash**~~ — **Resolved:** `yoloai list` shows "done" (exit 0) vs "failed" (non-zero) using tmux `pane_dead_status`. Not just "exited."

58. ~~**`yoloai list` doesn't show unapplied changes**~~ — **Deferred.** Running `git diff --stat` for every sandbox on every `list` is a performance risk (especially overlay requiring running container). `yoloai diff --stat <name>` is the fast check.

59. ~~**Multiple sandbox conflict detection is absent**~~ — **Resolved:** Include better error messaging — wrap `git apply` failures with context explaining why the patch failed. Predictive conflict detection deferred.

60. ~~**No bulk destroy or stop**~~ — **Resolved:** `yoloai destroy name1 name2 name3` with single confirmation, plus `--all` flag. Same for `yoloai stop`.

61. ~~**First-time base image build is slow and poorly communicated**~~ — **Resolved:** Clear "Building base image (first run only, ~2-5 minutes)..." message during auto-build on first `yoloai new`.

62. ~~**`yoloai log` has no tail or search**~~ — **Resolved:** ~~No `--tail`, no pager. Raw stdout output. User composes with unix tools.~~ Superseded by #76: auto-page through `$PAGER` / `less -R` when stdout is a TTY.

63. ~~**No way to see what prompt was given to a sandbox**~~ — **Resolved:** Include `yoloai show <name>` in MVP. Displays all sandbox details: name, status, agent, profile, prompt, workdir (resolved path), creation time, baseline SHA, container ID. Essential for dogfooding/debugging.

64. ~~**`YOLOAI_SANDBOX` is awkward for multi-sandbox workflows**~~ — **Resolved:** Documentation only. Document `YOLOAI_SANDBOX` as "useful for single-sandbox sessions" rather than general convenience.

65. ~~**`yoloai apply` on overlay requires container running**~~ — **Resolved:** `yoloai apply` auto-starts the container when needed for overlay diff. No user action required.

66. ~~**No `yoloai new --replace` for iterate-and-retry**~~ — **Resolved:** Include `--replace` flag on `yoloai new`. Destroys existing sandbox of the same name and creates fresh.

67. ~~**`yoloai reset` preserves agent-state, which may work against the user**~~ — **Resolved:** Include `--clean` flag on `yoloai reset` to wipe agent-state for a truly fresh start.

68. ~~**Workdir `.` has no confirmation of resolved path**~~ — **Resolved:** Already covered by existing creation output format showing resolved absolute path. No design change needed.

69. ~~**No inline prompt entry on `yoloai new` without `--prompt`**~~ — **Deferred.** `--prompt`, `--prompt-file`, and `--prompt -` (stdin) cover the bases. `$EDITOR` integration is polish for post-MVP.

70. ~~**No `yoloai diff` safety note while agent is running**~~ — **Resolved:** Print warning "Note: agent is still running; diff may be incomplete" when tmux pane is alive during `yoloai diff`.

71. ~~**No way to inspect profile configuration**~~ — **Deferred.** Profiles are post-MVP. Add `yoloai profile show` when profiles are implemented.

72. ~~**Shell quoting for `--prompt` is painful**~~ — **Deferred.** Same as #69. `--prompt-file` and stdin already address the pain. `--edit` for `$EDITOR` deferred.

73. ~~**`yoloai destroy` confirms even when unnecessary**~~ — **Resolved:** Smart destroy confirmation — skip prompt when sandbox is stopped/exited with no unapplied changes. Only confirm when agent is running or unapplied changes exist.

74. ~~**No warning when `:rw` workdir overlaps with existing sandbox**~~ — **Resolved:** Error at creation time on path prefix overlap between any sandbox mounts — `:rw`/`:rw`, `:rw`/`:copy`, `:copy`/`:copy`. Check: does either resolved path start with the other? `:force` suffix overrides with a warning (same mechanism as dangerous directory detection). Error by default, `:force` is the explicit escape hatch.

75. ~~**Codex follow-up limitation undocumented**~~ — **Deferred.** Codex is post-MVP. Document the session persistence limitation when Codex is implemented.

## UX Issues — Round 3 (from workflow simulation)

76. ~~**`yoloai diff` and `yoloai log` should auto-page when stdout is a TTY**~~ — **Resolved:** `yoloai diff` and `yoloai log` should auto-page through `$PAGER` / `less -R` when stdout is a TTY, matching `git diff`/`git log` behavior. Piping (`yoloai diff my-task | less`) already works since both output raw to stdout; auto-paging is the polished default.

77. **No `yoloai wait` command for scripting/CI** — **Deferred.** No built-in way to block until the agent finishes — must poll `yoloai list --json`. A `yoloai wait <name> [--timeout <duration>]` that blocks until agent exit (returning the agent's exit code) would enable CI workflows. Related to deferred `yoloai run` (#56) — `run` is sugar on top of `wait`. Post-MVP.

78. ~~**Multiple `:copy` sandboxes from same source — sequential apply conflicts**~~ — **Removed.** The "compare two approaches in parallel" scenario is contrived — in practice you'd use `reset` or `--replace` to iterate sequentially. Accidental overlap (forgot a sandbox exists) is already covered by `git apply` error wrapping (#59).

79. ~~**`yoloai apply` auto-starting container for overlay should print a message**~~ — **Resolved:** Print "Starting container for overlay diff..." to stderr when auto-starting a stopped container during `yoloai apply`. Consistent with CLI-STANDARD.md progress-on-stderr convention.

80. ~~**Cannot add `--port` after sandbox creation**~~ — **Resolved:** Docker limitation — port mappings cannot be added to running containers. Document in `--port` help text: "Ports must be specified at creation time. To add ports later, use `yoloai new --replace`." No code change, just documentation.

81. ~~**`:rw` diff shows all uncommitted changes, not just agent changes**~~ — **Resolved:** Inherent to `:rw` mode — `git diff` runs against HEAD on the live directory, so pre-existing uncommitted changes are mixed with agent changes. Document in `yoloai diff` help: "For `:rw` directories, diff shows all uncommitted changes relative to HEAD, not just agent changes. Use `:copy` mode for clean agent-only diffs."

82. ~~**Post-creation output should adapt to whether `--prompt` was given**~~ — **Resolved:** Context-aware next-command suggestions after `yoloai new`: without `--prompt`, suggest `yoloai attach <name>` (agent is waiting for input); with `--prompt`, suggest `yoloai attach <name>` to interact and `yoloai diff <name>` when done.

83. ~~**`yoloai new` output should show resolved configuration**~~ — **Resolved:** Creation output shows a brief summary of resolved settings: agent, profile (or "base"), workdir path + mode, copy strategy, network mode. Confirms what was actually configured when options come from defaults + profile + CLI.

84. ~~**`show` and `status` commands overlap**~~ — **Resolved:** Merge into single `yoloai show` command. `show` now includes directories with access modes (from `status`). `status` removed from command table.

85. ~~**Entrypoint JSON parsing**~~ — **Resolved:** Install `jq` in the base image. The entrypoint reads `/yoloai/config.json` via `jq` for all configuration (agent_command, startup_delay, submit_sequence, host_uid, host_gid, etc.). Simpler and more robust than shell-only JSON parsing.

86. ~~**Agent CLI arg passthrough**~~ — **Resolved:** `yoloai new fix-bug . -- --max-turns 5` passes everything after `--` verbatim to the agent command. Appended to agent_command in config.json. First-class flags (`--model`) take precedence if duplicated. Standard `--` convention (npm, docker, cargo). High value for dogfooding — agents have many flags yoloai doesn't need to wrap.

## Post-MVP (Codex and cleanup)

37. **Codex proxy support** — Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified (DESIGN.md line 340, RESEARCH.md). Critical for `--network-isolated` mode with Codex. If it ignores proxy env vars, would need iptables-only enforcement.

38. **Codex required network domains** — Only `api.openai.com` is confirmed (DESIGN.md line 341/445). Additional domains (telemetry, model downloads) may be required.

39. **Codex TUI behavior in tmux** — Interactive mode (`codex --yolo` without `exec`) behavior in tmux is unverified (RESEARCH.md).

40. **Image cleanup mechanism** — Docker images accumulate indefinitely. Cleanup is deferred pending research into Docker's image lifecycle (DESIGN.md line 642). Needs design for safe pruning that doesn't break running sandboxes.
