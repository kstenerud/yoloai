# Plan: yoloai MVP for Dogfooding

## Context

No code exists yet. All design docs are complete (DESIGN.md, CODING-STANDARD.md, CLI-STANDARD.md). All pre-implementation questions resolved (see OPEN_QUESTIONS.md #1–85). The goal is a working MVP that can dogfood — run `yoloai new fix-build --prompt "fix the build" ~/Projects/yoloai:copy`, have Claude Code work inside a container, then `yoloai diff` / `yoloai apply` to review and land changes.

## What's In / What's Deferred

**MVP commands:** `build`, `new`, `attach`, `show`, `diff`, `apply`, `list`, `log`, `tail`, `exec`, `stop`, `start`, `destroy`, `reset`, `completion`, `version`

**MVP features:** Full-copy only (Claude only), credential injection, `--model`, `--prompt-file`/stdin, `--replace`, `--no-start`, `--stat` on diff, `--yes` on apply/destroy, `--no-prompt`/`--clean` on reset, `--all`/multi-name on stop/destroy, smart destroy confirmation, dangerous directory detection, dirty git repo warning, path overlap detection, `YOLOAI_SANDBOX` env var, context-aware creation output, auto-paging for diff/log, shell completion, version info.

**Deferred:** overlay strategy, network isolation/proxy, profiles, Codex agent, Viper config file parsing, `auto_commit_interval`, custom mount points (`=<path>`), `agent_files`, env var interpolation, context file, aux dirs (`-d`), `--resume`, `restart`, `wait`, `run`.

## Implementation Phases

### Phase 0: Project Scaffold

Compilable Go project with Cobra CLI that prints help text. No Docker, no functionality.

**Create:**
- `go.mod` (`github.com/kstenerud/yoloai`), `Makefile` (`build`, `test`, `lint`), `.golangci.yml`
- `cmd/yoloai/main.go` — thin entry point, `signal.NotifyContext`, calls root command
- `internal/cli/root.go` — root Cobra command, `SilenceErrors: true`, `SilenceUsage: true`, custom error→exit code mapping (0/1/2/3)
- `internal/cli/*.go` — stub commands for: `build`, `new`, `attach`, `show`, `diff`, `apply`, `list`, `log`, `tail`, `exec`, `stop`, `start`, `destroy`, `reset`, `completion`, `version`

**Verify:** `go build ./...` compiles, `./yoloai --help` shows all commands with correct descriptions.

### Phase 1: Core Domain Types and Path Encoding

Pure Go, fully unit-testable without Docker.

**Create:**

`internal/sandbox/paths.go`:
- `EncodePath(hostPath string) string` — full [caret encoding](https://github.com/kstenerud/caret-encoding) spec (not just `/` → `^2F`)
- `DecodePath(encoded string) string` — reverse
- `SandboxDir(name string) string` — `~/.yoloai/sandboxes/<name>/`
- `WorkDir(name string, hostPath string) string` — `.../work/<encoded-path>/`

`internal/sandbox/meta.go`:
- `Meta` struct matching simplified MVP meta.json (no network, no directories array, no ports/resources)
- `SaveMeta(path, meta)`, `LoadMeta(path)`

`internal/agent/agent.go`:
- `Definition` struct: Name, InteractiveCmd, APIKeyEnvVars, StateDir, SubmitSequence, StartupDelay
- `GetAgent(name string)` — returns Claude definition

`internal/sandbox/parse.go`:
- `ParseDirArg(arg string) (path, mode string, force bool, err error)` — splits path from `:copy`/`:rw`/`:force` suffixes

**Verify:** `go test ./internal/sandbox/... ./internal/agent/...` — table-driven tests for encoding round-trips (full caret spec), meta.json serialization, dir arg parsing (including `:force` combinations).

### Phase 2: Docker Client Wrapper

**Create:**

`internal/docker/client.go`:
- `Client` interface wrapping the Docker SDK methods we need (ImageBuild, ContainerCreate/Start/Stop/Remove/List/Inspect, ContainerExecCreate/Attach/Inspect, Ping, Close)
- `NewClient(ctx) (Client, error)` — creates real client, pings Docker, returns clear error if unavailable

**Verify:** Compile check. Integration test (build-tagged) verifies Docker connectivity.

### Phase 3: Base Image and `yoloai build`

**Create:**

`resources/Dockerfile.base`:
- `FROM debian:bookworm-slim`
- Install: tmux, git, build-essential, python3, curl, ca-certificates, gnupg, jq
- Node.js 20 LTS via NodeSource
- Claude Code: `npm install -g @anthropic-ai/claude-code`
- gosu from GitHub releases
- Create user `yoloai` (UID 1001 placeholder)
- Create `/yoloai/` directory
- Copy entrypoint script

`resources/entrypoint.sh` (~50 lines):
1. Run as root: `usermod -u $(jq -r .host_uid /yoloai/config.json) yoloai`, `groupmod -g $(jq -r .host_gid /yoloai/config.json) yoloai` (handle exit code 12)
2. Fix ownership on `/yoloai` and home dir
3. Read `/run/secrets/*`, export filename=content as env vars
4. Drop to yoloai user via gosu
5. Start tmux session `main` with `pipe-pane` to `/yoloai/log.txt` and `remain-on-exit on`
6. Inside tmux: launch agent command from config.json (`jq -r .agent_command /yoloai/config.json`)
7. If `/yoloai/prompt.txt` exists: sleep `$(jq -r .startup_delay /yoloai/config.json)`, `tmux load-buffer` + `tmux paste-buffer` + `tmux send-keys` with submit sequence from config.json (`jq -r .submit_sequence /yoloai/config.json`)
8. Wait for tmux session to end

**`/yoloai/config.json` fields (MVP):**
- `host_uid` (int) — host user UID
- `host_gid` (int) — host user GID
- `agent_command` (string) — full agent launch command
- `startup_delay` (int) — seconds to wait before sending prompt
- `submit_sequence` (string) — tmux send-keys sequence (e.g., `"Enter Enter"`)

Later additions (post-MVP): `overlay_mounts`, `iptables_rules`, `setup_script`.

`internal/docker/build.go`:
- `BuildBaseImage(ctx, client, logger)` — creates build context tar from `resources/`, calls `client.ImageBuild`, streams output

Wire `yoloai build` command.

**Verify:** `yoloai build` produces `yoloai-base` image. `docker run --rm --init yoloai-base claude --version` works.

### Phase 4: Sandbox Creation (`yoloai new`)

The largest phase — implements the core creation workflow.

**Create:**

`internal/sandbox/errors.go`:
- `ErrSandboxNotFound`, `ErrSandboxExists`, `ErrDockerUnavailable`, `ErrMissingAPIKey`
- `UsageError` type (exit 2), `ConfigError` type (exit 3)

`internal/sandbox/safety.go`:
- `IsDangerousDir(path string) bool` — resolves symlinks (`filepath.EvalSymlinks`) then checks against `$HOME`, `/`, macOS system dirs, Linux system dirs
- `CheckPathOverlap(dirs []DirMount) error` — resolves symlinks then checks if any two resolved paths have prefix overlap
- `CheckDirtyRepo(path string) (warning string, err error)` — checks for uncommitted git changes

`internal/sandbox/manager.go`:
- `SandboxManager` struct (holds docker.Client, slog.Logger)
- `Create(ctx, CreateOptions) error`:
  1. Parse workdir arg — extract path, resolve to absolute, validate `:copy`/`:rw`/`:force`
  2. Run safety checks: dangerous directory detection, path overlap detection
  3. Validate: name non-empty, no duplicate sandbox (unless `--replace`), workdir exists, ANTHROPIC_API_KEY set
  4. If `--replace`, destroy existing sandbox first
  5. Dirty git repo warning — prompt for confirmation if uncommitted changes detected
  6. Create dir structure: `~/.yoloai/sandboxes/<name>/`, `work/`, `agent-state/`
  7. `cp -a` workdir to `work/<encoded-path>/`
  8. Git baseline: if `.git/` exists record HEAD SHA, else `git init + git add -A + git commit`
  9. Always store baseline SHA in meta.json
  10. Write meta.json, prompt.txt (from `--prompt`, `--prompt-file`, or stdin), empty log.txt
  11. Generate `/yoloai/config.json` with host_uid, host_gid, agent_command (including `--model` if specified), startup_delay, submit_sequence
  12. Create API key temp file (defer cleanup)
  13. If `--no-start`, stop here — print creation output and exit
  14. Create + start Docker container with mounts:
      - `work/<encoded-path>/` → mirrored host path (rw)
      - `agent-state/` → `/home/yoloai/.claude/` (rw)
      - `log.txt` → `/yoloai/log.txt` (rw)
      - `prompt.txt` → `/yoloai/prompt.txt` (ro, if exists)
      - `config.json` → `/yoloai/config.json` (ro)
      - temp key file → `/run/secrets/ANTHROPIC_API_KEY` (ro)
  15. Container config: image `yoloai-base`, name `yoloai-<name>`, `--init`, working dir = mirrored host path
  16. Clean up temp key file
  17. Print context-aware creation output

Wire `yoloai new` — parse `--prompt`, `--prompt-file`, `--model`, `--replace`, `--no-start`, name, workdir positional args.

**Creation output (with prompt):**
```
Sandbox fix-bug created
  Agent:    claude
  Workdir:  /home/user/projects/my-app (copy)

Run 'yoloai tail fix-bug' to watch progress
    'yoloai attach fix-bug' to interact
    'yoloai diff fix-bug' when done
```

**Creation output (without prompt):**
```
Sandbox explore created
  Agent:    claude
  Workdir:  /home/user/projects/my-app (copy)

Run 'yoloai attach explore' to start working
```

Profile and network lines omitted when using defaults. Strategy line omitted for full copy.

**Verify:** Unit tests for arg parsing, safety checks. Integration: `yoloai new test-sandbox /tmp/test-project:copy` creates sandbox, `docker ps` shows `yoloai-test-sandbox`.

### Phase 5: Inspection and Output

**`yoloai attach`:** `os/exec` → `docker exec -it yoloai-<name> tmux attach -t main`. (SDK doesn't handle raw TTY well for interactive tmux — justified exception to "use SDK not CLI".)

**`yoloai show`:** Load `meta.json`, query Docker for container state. Display: name, status (running/stopped/done/failed), agent, profile (or "(base)"), prompt (first 200 chars), workdir, directories with access modes, creation time, baseline SHA, container ID. Agent status detected via `docker exec tmux list-panes -t main -F '#{pane_dead}'` combined with Docker container state. Done (exit 0) vs failed (non-zero) via `pane_dead_status`.

**`yoloai list`:** Scan sandboxes dir, load meta.json for each, query Docker for status. Format table: NAME | STATUS | AGENT | AGE | WORKDIR. Status uses same done/failed detection as `show`.

**`yoloai log`:** Read `~/.yoloai/sandboxes/<name>/log.txt`. Auto-page through `$PAGER` / `less -R` when stdout is a TTY. Raw output when piped.

**`yoloai tail`:** Tail `log.txt` in real time (like `tail -f`).

**`yoloai exec`:** `docker exec yoloai-<name> <command>`, with `-i` when stdin is pipe/TTY and `-t` when stdin is TTY.

`internal/cli/pager.go`:
- `RunPager(r io.Reader) error` — pipes content through `$PAGER` / `less -R` when stdout is TTY, otherwise copies to stdout. Used by `diff` and `log`.

**Verify:** Manual test after Phase 4 sandbox creation.

### Phase 6: Diff and Apply

The core differentiator.

**`yoloai diff`:**
- Load meta.json, get baseline SHA
- Run `git add -A` (capture untracked files) then `git diff <baseline_sha>` in `work/<encoded-path>/`
- If original had no `.git/`, exclude synthetic git dir: `git diff <baseline_sha> -- ':!.git'`
- Use `git diff --binary` to capture binary file changes in patch
- Auto-page through `$PAGER` / `less -R` when stdout is TTY
- Print warning "Note: agent is still running; diff may be incomplete" when tmux pane is alive
- `--stat`: pass through to `git diff --stat`

**`yoloai apply`:**
- `yoloai apply <name> [-- <path>...]`
- Generate patch: `git diff --binary <baseline_sha>` (same baseline logic as diff)
- If empty: "No changes to apply", exit
- Show summary via `git diff --stat <baseline_sha>`
- Dry-run: `git apply --check` on original host dir
- Prompt: `Apply these changes to /path/to/original? [y/N]` (skip with `--yes`)
- Apply: `git apply` on original host dir
- On failure: wrap `git apply` error with context explaining why (e.g., "changes to handler.go conflict with changes already in your working directory — the patch expected line 42 to be 'func foo()' but found 'func bar()'")
- `[-- <path>...]`: apply only changes to specified files/directories (relative to workdir)

**Key subtlety:** Always store baseline SHA in meta.json (Phase 4 step 9). For original git repos, it's the HEAD at copy time. For non-git dirs, it's the SHA of the synthetic initial commit. Diff is always `git diff <baseline_sha>`.

**Verify:** Create sandbox, make a change in the copy manually, `yoloai diff` shows it, `yoloai apply --yes` lands it.

### Phase 7: Lifecycle (stop/start/destroy/reset)

**`yoloai stop`:** Docker SDK `ContainerStop`. Accepts multiple names (e.g., `yoloai stop s1 s2 s3`). `--all` flag stops all running sandboxes. Print confirmation per sandbox.

**`yoloai start`:** Check state — if running: no-op; if stopped: `ContainerStart`; if container removed: recreate from meta.json (skip copy step, create new credential temp file). Print confirmation.

**`yoloai destroy`:** Accepts multiple names (e.g., `yoloai destroy s1 s2 s3`). `--all` flag. Smart confirmation: only prompt when agent is still running or unapplied changes exist (check via `git diff` on `:copy` dirs). `--yes` skips all confirmation. `docker stop` + `docker rm` + `os.RemoveAll` sandbox dir.

**`yoloai reset`:** Re-copy workdir from original host directory, reset git baseline. Container stopped and restarted. `meta.json` preserved. By default re-sends original prompt from `prompt.txt`. Options: `--no-prompt` (skip re-sending prompt), `--clean` (also wipe `agent-state/` for full reset).

**Verify:** Full lifecycle: new → stop → start → destroy. Reset: new with prompt → make changes → reset → verify clean workdir.

### Phase 8: Polish and Integration

**`yoloai completion`:** Wire Cobra's built-in `GenBashCompletionV2`, `GenZshCompletion`, `GenFishCompletion`, `GenPowerShellCompletion`.

**`yoloai version`:** Print version, commit, build date. Set via `ldflags` at build time: `-X main.version=... -X main.commit=... -X main.date=...`.

**`YOLOAI_SANDBOX` env var:** Support on all name-taking commands as default sandbox name. Explicit `<name>` argument always takes precedence. Check env var when name arg is empty.

**Integration test** (`internal/sandbox/integration_test.go`, build tag `//go:build integration`):
1. Create temp dir with simple Go project
2. Build base image (or verify exists)
3. Create sandbox with prompt
4. Wait for agent activity (poll log.txt)
5. Diff, apply, destroy
6. Verify original was modified

**Dogfood:** `yoloai new fix-something --prompt "fix the build" ~/Projects/yoloai:copy`

## Architecture Decisions

- **`SandboxManager`** is the central orchestrator. CLI commands are thin (parse args → call manager method).
- **git via `os/exec`** — simpler than go-git for our needs (diff, apply, init, add, commit, rev-parse).
- **`docker exec` for attach via `os/exec`** — justified exception to "use SDK" rule for interactive TTY.
- **No Viper for MVP** — CLI flags + hardcoded defaults. Config file parsing comes post-MVP.
- **Entrypoint as shell script** — natural for UID/GID, secrets, tmux. ~50 lines. Go binary would add cross-compilation complexity.
- **`jq` in base image** — entrypoint reads `/yoloai/config.json` via `jq` for all configuration. Simpler and more robust than shell-only JSON parsing.
- **Pager utility** (`internal/cli/pager.go`) — reusable auto-paging for `diff` and `log`. Uses `$PAGER` / `less -R` when stdout is TTY, raw output when piped.
- **Reusable confirmation prompt** — shared helper for apply, destroy, and dirty repo warning confirmations.
- **Error types** map to exit codes in root command handler.

## Dependencies

```
github.com/spf13/cobra         # CLI framework
github.com/docker/docker        # Docker SDK
github.com/stretchr/testify     # Test assertions (dev)
```

Viper deferred to post-MVP.

## File Inventory

| File | Purpose |
|------|---------|
| `go.mod` | Module definition |
| `Makefile` | build, test, lint targets |
| `.golangci.yml` | Linter config |
| `cmd/yoloai/main.go` | Entry point |
| `internal/cli/root.go` | Root command, error→exit code |
| `internal/cli/pager.go` | Auto-paging utility (diff, log) |
| `internal/cli/{build,new,attach,show,diff,apply,list,log,tail,exec,stop,start,destroy,reset,completion,version}.go` | Command definitions |
| `internal/sandbox/paths.go` | Caret encoding (full spec), dir layout |
| `internal/sandbox/meta.go` | meta.json types and I/O |
| `internal/sandbox/parse.go` | Dir arg parsing (`:copy`/`:rw`/`:force`) |
| `internal/sandbox/errors.go` | Error types for exit codes |
| `internal/sandbox/safety.go` | Dangerous dir, path overlap, dirty repo checks |
| `internal/sandbox/manager.go` | All sandbox operations |
| `internal/docker/client.go` | Docker SDK wrapper interface |
| `internal/docker/build.go` | Image build logic |
| `internal/agent/agent.go` | Agent definitions (Claude) |
| `resources/Dockerfile.base` | Base Docker image |
| `resources/entrypoint.sh` | Container entrypoint |
| Test files | `*_test.go` alongside each package |

## Risks

1. **Entrypoint fragility** — UID/GID + secrets + tmux + prompt delivery in one script. Mitigate: test manually early (Phase 3).
2. **tmux timing** — 3s delay may not suffice on slow machines. Mitigate: configurable via `config.json` `startup_delay`.
3. **Large copies** — `cp -a` with `node_modules` is slow. Known limitation of full-copy. Overlay (post-MVP) solves this.
4. **Docker SDK version compat** — Pin to known-good version, test on Docker Desktop for Mac.
