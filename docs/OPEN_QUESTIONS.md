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

## Post-MVP (Codex and cleanup)

37. **Codex proxy support** — Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified (DESIGN.md line 340, RESEARCH.md). Critical for `--network-isolated` mode with Codex. If it ignores proxy env vars, would need iptables-only enforcement.

38. **Codex required network domains** — Only `api.openai.com` is confirmed (DESIGN.md line 341/445). Additional domains (telemetry, model downloads) may be required.

39. **Codex TUI behavior in tmux** — Interactive mode (`codex --yolo` without `exec`) behavior in tmux is unverified (RESEARCH.md).

40. **Image cleanup mechanism** — Docker images accumulate indefinitely. Cleanup is deferred pending research into Docker's image lifecycle (DESIGN.md line 642). Needs design for safe pruning that doesn't break running sandboxes.
