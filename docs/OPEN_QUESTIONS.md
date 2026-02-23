# Open Questions

Questions encountered during design and implementation that need resolution. Resolve each before the relevant implementation phase begins.

## Pre-Implementation (resolve before coding starts)

1. ~~**Go module path**~~ — **Resolved:** `github.com/kstenerud/yoloai`.

2. ~~**Node.js version**~~ — **Resolved:** Node.js 20 LTS via NodeSource. Anthropic's own devcontainer uses Node 20 + npm install. The native Claude Code installer (curl script) is not suitable: bundles Bun with broken proxy support (issue #14165), segfaults on Debian bookworm AMD64 (#12044), and auto-updates. npm install shows a deprecation warning but remains the only reliable path for Docker/proxy use. See RESEARCH.md "Claude Code Installation Research".

3. ~~**tini**~~ — **Resolved:** Use `docker run --init` (Docker's built-in tini). Simpler than installing in image. We control all container creation in code so the flag is always passed.

4. ~~**gosu**~~ — **Resolved:** Install from GitHub releases (static binary). Standard for Debian images.

5. ~~**Claude ready indicator**~~ — **Resolved:** Fixed 3-second delay for MVP, configurable via `YOLOAI_STARTUP_DELAY`. Polling deferred.

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

42. ~~**Implicit workdir from cwd**~~ — **Resolved:** Keep workdir explicit (`.` required). One character is low friction and avoids accidental sandboxing of wrong directory.

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

53. **No read-only/investigation mode shortcut** — User wants Claude to investigate something without changing code, but `:copy` copies the whole project. No quick way to say "mount read-only, just let the agent look." `:rw` avoids the copy but semantically means writes are allowed. A `--read-only` flag or bare `:ro` suffix on workdir would let investigation tasks skip the copy entirely.

54. **`yoloai reset` does not re-send the prompt** — After reset, the agent restarts but the original prompt is not re-sent. User has to attach and manually re-type. `reset` should re-feed the original prompt by default (the whole point is "try again"), with `--no-prompt` to suppress.

55. **No way to send a new prompt without attaching** — After reset or start, user may want to provide a different prompt without attaching interactively. No `yoloai prompt <name> "new instructions"` command. Breaks scriptability and adds friction to iterate-and-retry loops.

56. **Quick successive tasks have too much ceremony** — Three small bug fixes require 12 commands (new, apply, destroy ×3). A `yoloai run . --prompt "fix bug"` that creates a temp sandbox, waits for completion, shows diff, prompts for apply, and auto-destroys would cut this to 3 commands.

57. **No indication of agent completion vs. crash** — `yoloai list` shows "exited" but doesn't distinguish clean exit from crash. `pane_dead_status` from tmux provides the exit code. `yoloai list` should show "done" vs "failed" (or at least the exit code).

58. **`yoloai list` doesn't show unapplied changes** — User with multiple sandboxes must run `yoloai diff` on each to find which have changes. A `CHANGES` column (e.g., `+15 -3` or `3 files`) in `yoloai list` would save multiple commands.

59. **Multiple sandbox conflict detection is absent** — Two sandboxes copy the same project and modify overlapping files. `git apply --check` catches conflicts, but the error should explain why ("changes to handler.go conflict with changes already applied") rather than raw git output. No way to predict conflicts before applying.

60. **No bulk destroy or stop** — Cleaning up multiple sandboxes requires individual `yoloai destroy` with confirmation each time. `yoloai destroy name1 name2 name3` with single confirmation, or `yoloai destroy --all`, would streamline cleanup.

61. **First-time base image build is slow and poorly communicated** — First `yoloai new` triggers a 2-5 minute Docker build. User expects to start working and sees scrolling Docker output. Should clearly state "Building base image (first run only, 2-5 minutes)" with progress indication.

62. **`yoloai log` has no tail or search** — For 30+ minute tasks, the log is thousands of lines. No `--tail N` option (only `yoloai tail` for live following). Adding `--tail N` and defaulting to a pager when stdout is a TTY would help.

63. **No way to see what prompt was given to a sandbox** — With multiple sandboxes, user forgets what each was asked to do. `yoloai list` shows name and workdir but not the prompt. `yoloai status <name>` (or `yoloai show <name>`) should display the prompt (or first 80 chars).

64. **`YOLOAI_SANDBOX` is awkward for multi-sandbox workflows** — With three active sandboxes, `YOLOAI_SANDBOX=fix-bug-1 yoloai diff` is more typing than `yoloai diff fix-bug-1`. The env var only helps single-sandbox sessions. Document accordingly rather than treating it as general convenience.

65. **`yoloai apply` on overlay requires container running** — Overlay apply needs the merged view, so the container must be up. If user stops the container first (natural when "done"), then tries to apply, it fails. `yoloai apply` should auto-start the container when needed for overlay, or the error should clearly explain and suggest `yoloai start` first.

66. **No `yoloai new --replace` for iterate-and-retry** — User repeatedly creates sandboxes with the same name for the same project, differing only in prompt. Each time: destroy old, create new. A `--replace` flag that destroys the existing sandbox first would streamline this to one command.

67. **`yoloai reset` preserves agent-state, which may work against the user** — Reset re-copies workdir but preserves agent-state (session history). Agent remembers its previous failed attempt and may repeat mistakes. No `--clean` flag to wipe agent-state for a truly fresh start. Only option is destroy + new.

68. **Workdir `.` has no confirmation of resolved path** — If user runs `yoloai new fix-bug .` from the wrong directory, sandbox gets the wrong project. The creation output should show the resolved absolute path: `Creating sandbox 'fix-bug' for /home/user/projects/my-app...` to catch mistakes.

69. **No inline prompt entry on `yoloai new` without `--prompt`** — If user forgets `--prompt`, they get an interactive session and must attach. More natural for quick tasks: prompt inline at creation. Consider `--edit` flag to open `$EDITOR` (like `git commit` without `-m`).

70. **No `yoloai diff` safety note while agent is running** — `yoloai diff` while agent is actively writing shows a point-in-time snapshot that may include partial changes. Not dangerous (git handles this), but the output should note "agent is still running; diff may be incomplete."

71. **No way to inspect profile configuration** — User can't remember what workdir or directories a profile configures without reading the YAML file manually. `yoloai profile show <name>` would eliminate this friction.

72. **Shell quoting for `--prompt` is painful** — Multi-line or complex prompts require shell escaping. `--prompt-file` helps but requires a temp file. `--edit` flag to open `$EDITOR` (like `git commit` without `-m`) would be the most ergonomic for non-trivial prompts.

73. **`yoloai destroy` confirms even when unnecessary** — Destroying a stopped sandbox with no unapplied changes still prompts. Confirmation should only trigger when agent is running or changes exist. Otherwise destroy immediately.

74. **No warning when `:rw` workdir overlaps with existing sandbox** — Two sandboxes with `:rw` access to the same directory cause data races. Should warn when new sandbox's `:rw` workdir overlaps with an existing sandbox.

75. **Codex follow-up limitation undocumented** — Codex uses headless `exec` mode with no session persistence. After exit, user can't ask follow-up questions. Should document this and suggest Claude for iterative workflows.

## Post-MVP (Codex and cleanup)

37. **Codex proxy support** — Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified (DESIGN.md line 340, RESEARCH.md). Critical for `--network-isolated` mode with Codex. If it ignores proxy env vars, would need iptables-only enforcement.

38. **Codex required network domains** — Only `api.openai.com` is confirmed (DESIGN.md line 341/445). Additional domains (telemetry, model downloads) may be required.

39. **Codex TUI behavior in tmux** — Interactive mode (`codex --yolo` without `exec`) behavior in tmux is unverified (RESEARCH.md).

40. **Image cleanup mechanism** — Docker images accumulate indefinitely. Cleanup is deferred pending research into Docker's image lifecycle (DESIGN.md line 642). Needs design for safe pruning that doesn't break running sandboxes.
