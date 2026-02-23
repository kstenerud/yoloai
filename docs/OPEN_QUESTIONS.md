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

15. **Entrypoint configuration passing** — The entrypoint needs to know: agent command, startup delay, submit sequence, overlay mount info (list of lowerdir/upperdir/mountpoint tuples), iptables rules, setup commands. Some are env vars (HOST_UID, HOST_GID, YOLOAI_AGENT_CMD). How are overlay mount info, iptables rules, and setup commands passed? Env vars? A bind-mounted config file?

16. **`setup` commands — execution mechanism** — "Run setup commands from config (if any)" — how do they get into the container? Passed as env var? Written to a script and bind-mounted? Run by the entrypoint or by yoloai via docker exec after container start?

17. **tmux behavior when agent exits** — If the agent completes or crashes, does tmux exit (bringing down the container)? Or stay alive with `remain-on-exit`? Affects: `yoloai list` STATUS detection, `yoloai start` "agent exited but container running" case, and whether log capture continues.

18. **Context file content and delivery** — The design says "generate a sandbox context file" but doesn't specify the template/format. For Claude: `--append-system-prompt`. For Codex: "inclusion in the initial prompt." Does the entrypoint prepend context.md to prompt.txt? Or is it a separate mechanism?

### Diff / Apply

19. **Untracked files in diff/apply** — If the agent creates new files but doesn't `git add` them, `git diff <baseline>` won't include them. Apply generates a patch via `git diff` — untracked files excluded. Should we `git add -A` before diffing to capture everything?

20. **Multiple `:copy` directories in diff/apply** — Both workdir and aux dirs can be `:copy`. Does diff show all? With headers? Does apply run all at once or per-directory with separate confirmations? What if one apply succeeds but another fails?

21. **Overlay apply — patch transfer to host** — Overlay apply runs `git diff` inside the container via docker exec. The patch needs to reach the host for `git apply`. Via docker exec stdout? A temp file in a shared mount?

### Agent Files

22. **`agent_files: home` — scope** — Copy the entire `~/.claude/` directory? Just top-level files? Including subdirs and session history? What if the directory doesn't exist (user uses native installer)?

23. **`agent_files` — "first run" detection** — "Copied into agent-state/ on first run." Keyed on agent-state/ being empty? A marker file? What if the user wants to re-seed?

### Build / Resources

24. **How does the binary find Dockerfile and entrypoint at runtime?** They live in `resources/` during development. The shipped binary is standalone — are these embedded via `go:embed`? Or must they exist on disk?

25. **Codex binary download URL and versioning** — "Static Rust binary download" — from where? What URL? How is the version pinned? Is this in the Dockerfile?

26. **`yoloai build --secret` — which secrets are automatically provided?** The design says yoloai automatically provides `~/.npmrc`. Is this automatic for every build or opt-in? What other secrets are supported?

### Network Isolation

27. **Docker network naming and lifecycle** — `--internal` network — per-sandbox (`yoloai-<name>-net`)? Shared across sandboxes? Created/destroyed alongside the sandbox?

28. **Proxy allowlist file format** — "Loaded from a config file; reloadable via SIGUSR1" — what format? One domain per line? JSON? Where in the proxy container?

29. **Proxy Go source location** — Where does the ~200-300 line proxy live in the repo? `internal/proxy/`? `cmd/proxy/`? `resources/proxy/`?

### Lifecycle Edge Cases

30. **`yoloai start` when container was removed** — "Recreate from meta.json." Does this re-run full container creation logic minus the copy step? What about credential injection (temp file was already cleaned up)?

31. **`yoloai list` STATUS "exited" detection** — How to detect "agent exited but container up"? Docker exec + pgrep? Tmux session status check?

### Miscellaneous

32. **Dangerous directory list** — `$HOME`, `/`, and "system directories." What's the explicit list? Platform-specific (macOS has `/System`, `/Library`)?

33. **`yoloai diff` for `:rw` — why docker exec?** `:rw` is a bind mount — the host directory IS the container directory. We could just run `git diff` on the host directly. Is docker exec needed?

34. **No workdir and no profile** — Workdir is optional if profile provides one. What if neither provides a workdir? What error message?

35. **`auto_commit_interval` implementation** — "Background auto-commit loop inside the container." Shell script? Separate binary? Spawned by entrypoint? What commit message and author?

36. **Profile without a Dockerfile** — Can a profile have just `profile.yaml` and no Dockerfile (using base image)? Or is Dockerfile required?

## UX Issues (from workflow simulation)

Simulated common workflows (one-off fix, iterative dev, interactive session, first-time setup, profile-based daily use). These are friction points.

### High friction — affects every session

41. **`.:copy` is boilerplate for the most common case.** The overwhelming use case is "sandbox my current directory as a copy" — the entire point of the tool. Currently: `yoloai new fix-bug .:copy`. Typing `.:copy` on every invocation is friction. Should workdir default to `:copy` (since `:copy` IS the tool's philosophy)? Then `yoloai new fix-bug .` works. `:rw` would still require explicit opt-in, keeping the safe-default principle.

42. **Implicit workdir from cwd.** Related to above. When you're already in your project directory (common case), should `yoloai new fix-bug` without a workdir argument default to `.:copy`? That gives the minimal: `yoloai new fix-bug --prompt "fix the build"`. Currently this errors because no workdir and no profile. But "I'm in my project dir, sandbox it" is the primary workflow.

43. **Sandbox name typed 5-6 times per workflow.** `new`, `attach`, `diff`, `apply`, `destroy` — the name appears in every command. Shell tab-completion (Cobra supports this) is essential. Should be generated as part of `yoloai init` or documented prominently. Consider also: if only one sandbox exists, should commands default to it? e.g., `yoloai diff` with no name when there's only one sandbox.

44. **No `--prompt-file` or stdin prompt.** Long or complex prompts are painful as `--prompt "..."` on the command line. Shell quoting, line breaks, and escaping make this worse. Consider: `--prompt-file <path>` to read from a file, or `--prompt -` to read from stdin (pipe-friendly).

### Medium friction — affects common workflows

45. **No reset/retry workflow.** Iterative development means create → try → destroy → create again with the same options. Currently: retype the entire `yoloai new` command. Consider `yoloai reset <name>` — re-copy the workdir (restoring to original state) while keeping the same sandbox config. Avoids destroy + re-create cycle.

46. **First-time setup is multiple steps.** New user experience: install → `yoloai init` → wait for base image build (slow) → set API key → `yoloai new`. Three failure points before first use. Should `yoloai new` auto-detect missing setup and offer to run init + build inline? Or at least: detect no base image and say "Run `yoloai build` first" with a clear message, rather than a cryptic Docker error.

47. **No default profile in config.** If a user always works with one profile, they type `--profile go-dev` on every `yoloai new`. Config has `defaults.agent` but no `defaults.profile`. Should it?

48. **`yoloai diff` has no summary mode.** When the agent made many changes, full diff output is noisy. Consider `--stat` (like `git diff --stat`) for a quick overview of which files changed and how much, before diving into the full diff.

49. **`yoloai apply` is all-or-nothing.** No way to apply changes to specific files. If the agent changed 10 files and you only want 3 of them, you must apply all or none. Consider `yoloai apply <name> [-- <path>...]` to apply specific files only.

### Low friction — nice to have

50. **Shell completion setup.** Cobra has built-in completion generation (`yoloai completion bash/zsh/fish`). This is critical for the name-repetition problem but needs to be generated and installed. Should `yoloai init` offer to set up shell completion? Or at least print instructions?

51. **No quick "is it done?" check.** `yoloai list` shows "running" but that doesn't distinguish "agent is actively working" from "agent finished and is waiting for input." `yoloai tail` works but requires watching output. A status indicator for "agent idle" vs "agent working" would help but may be hard to detect.

52. **No way to re-use a prompt after destroy.** Once a sandbox is destroyed, the prompt is gone. In iterative workflows where you want to try the same task differently (different model, different profile), you retype the prompt. A prompt history or `yoloai new --like <old-name>` (clone config from existing/destroyed sandbox) could help, but this is probably over-engineering for v1.

## Post-MVP (Codex and cleanup)

37. **Codex proxy support** — Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified (DESIGN.md line 340, RESEARCH.md). Critical for `--network-isolated` mode with Codex. If it ignores proxy env vars, would need iptables-only enforcement.

38. **Codex required network domains** — Only `api.openai.com` is confirmed (DESIGN.md line 341/445). Additional domains (telemetry, model downloads) may be required.

39. **Codex TUI behavior in tmux** — Interactive mode (`codex --yolo` without `exec`) behavior in tmux is unverified (RESEARCH.md).

40. **Image cleanup mechanism** — Docker images accumulate indefinitely. Cleanup is deferred pending research into Docker's image lifecycle (DESIGN.md line 642). Needs design for safe pruning that doesn't break running sandboxes.
