# Plan: yoloai MVP for Dogfooding

## Context

No code exists yet. All design docs are complete (DESIGN.md, CODING-STANDARD.md, CLI-STANDARD.md). All pre-implementation questions resolved (see OPEN_QUESTIONS.md #1–90). The goal is a working MVP that can dogfood — run `yoloai new fix-build --prompt "fix the build" ~/Projects/yoloai:copy`, have Claude Code work inside a container, then `yoloai diff` / `yoloai apply` to review and land changes.

## What's In / What's Deferred

**MVP commands:** `build`, `new`, `attach`, `show`, `diff`, `apply`, `list`, `log`, `exec`, `stop`, `start`, `destroy`, `reset`, `completion`, `version`

**MVP features:** Full-copy only (Claude only), credential injection, `--model` (with built-in aliases), `--prompt-file`/stdin, `--replace`, `--no-start`, `--network-none`, `--port`, `--` agent arg passthrough, `--stat` on diff, `--yes` on apply/destroy, `--no-prompt`/`--clean` on reset, `--all`/multi-name on stop/destroy, smart destroy confirmation, dangerous directory detection, dirty git repo warning, path overlap detection, `YOLOAI_SANDBOX` env var, context-aware creation output, auto-paging for diff/log, shell completion, version info.

**Deferred:** overlay strategy, network isolation/proxy, profiles, Codex agent, Viper config file parsing, `auto_commit_interval`, custom mount points (`=<path>`), `agent_files`, env var interpolation, context file, aux dirs (`-d`), `--resume`, `restart`, `wait`, `run`, `tail`.

## Implementation Phases

### Phase 0: Project Scaffold

Compilable Go project with Cobra CLI that prints help text. No Docker, no functionality.

**Create:**
- `go.mod` (`github.com/kstenerud/yoloai`), `Makefile` (`build`, `test`, `lint`), `.golangci.yml`
- `cmd/yoloai/main.go` — thin entry point, `signal.NotifyContext`, calls root command
- `internal/cli/root.go` — root Cobra command, `SilenceErrors: true`, `SilenceUsage: true`, custom error→exit code mapping (0/1/2/3)
- `internal/cli/*.go` — stub commands for: `build`, `new`, `attach`, `show`, `diff`, `apply`, `list`, `log`, `exec`, `stop`, `start`, `destroy`, `reset`, `completion`, `version`

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
- `Meta` struct matching simplified MVP meta.json (no directories array, no resources). Includes `NetworkMode` field (`""` for default, `"none"` for `--network-none`) and `Ports` field (`[]string`, e.g. `["3000:3000"]`) so `yoloai start` can reconstruct the container correctly.
- `SaveMeta(path, meta)`, `LoadMeta(path)`

`internal/agent/agent.go`:
- `Definition` struct: Name, InteractiveCmd, APIKeyEnvVars, StateDir, SubmitSequence, StartupDelay, ModelAliases (map[string]string for alias resolution, e.g. `"sonnet"` → `"claude-sonnet-4-latest"`)
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
- Node.js 22 LTS via NodeSource
- Claude Code: `npm install -g @anthropic-ai/claude-code`
- gosu from GitHub releases
- Create user `yoloai` (UID 1001 placeholder)
- Create `/yoloai/` directory
- Copy entrypoint script

`resources/entrypoint.sh` (~50 lines):
1. Run as root: `usermod -u $(jq -r .host_uid /yoloai/config.json) yoloai`, `groupmod -g $(jq -r .host_gid /yoloai/config.json) yoloai` (exit code 12 = "can't update /etc/passwd" — check if UID already matches desired, if so treat as no-op; otherwise log warning and continue)
2. Fix ownership on `/yoloai` and home dir
3. Read `/run/secrets/*`, export filename=content as env vars
4. Drop to yoloai user via gosu
5. Start tmux session `main` with `pipe-pane` to `/yoloai/log.txt` and `remain-on-exit on`
6. Inside tmux: launch agent command from config.json (`jq -r .agent_command /yoloai/config.json`)
7. If `/yoloai/prompt.txt` exists: sleep `$(jq -r .startup_delay /yoloai/config.json)`, `tmux load-buffer` + `tmux paste-buffer` + `tmux send-keys` with submit sequence from config.json (`jq -r .submit_sequence /yoloai/config.json`)
8. Wait indefinitely to keep container alive: `exec tmux wait-for yoloai-exit` (blocks forever since nothing signals this channel; container only stops on explicit `docker stop`). With `remain-on-exit on`, the tmux session persists even after the agent exits.

**`/yoloai/config.json` fields (MVP):**
- `host_uid` (int) — host user UID
- `host_gid` (int) — host user GID
- `agent_command` (string) — full agent launch command
- `startup_delay` (int) — seconds to wait before sending prompt
- `submit_sequence` (string) — tmux send-keys sequence (e.g., `"Enter Enter"`)

Later additions (post-MVP): `overlay_mounts`, `iptables_rules`, `setup_script`.

`internal/docker/build.go`:
- `SeedResources(targetDir string)` — copies embedded `resources/Dockerfile.base` and `resources/entrypoint.sh` to `targetDir` (i.e., `~/.yoloai/`) if they don't already exist. Uses `go:embed` for the source files.
- `BuildBaseImage(ctx, client, logger)` — creates build context tar from `~/.yoloai/` (Dockerfile.base + entrypoint.sh), calls `client.ImageBuild`, streams output. Always reads from `~/.yoloai/`, never from embedded copies — users can edit these files for fast iteration without rebuilding yoloai.

Wire `yoloai build` command. Call `SeedResources` before building.

**Verify:** `yoloai build` produces `yoloai-base` image. `docker run --rm --init yoloai-base claude --version` works. Also verify `--init` (tini PID 1) + `exec tmux wait-for` interaction: `docker stop` should send SIGTERM through tini to `tmux wait-for`, causing clean container shutdown.

### Phase 4a: Sandbox Infrastructure

Error types, safety checks, manager struct, and first-run setup — all testable independently before the creation workflow.

`internal/sandbox/errors.go`:
- `ErrSandboxNotFound`, `ErrSandboxExists`, `ErrDockerUnavailable`, `ErrMissingAPIKey`
- `UsageError` type (exit 2), `ConfigError` type (exit 3)

`internal/sandbox/safety.go`:
- `IsDangerousDir(path string) bool` — resolves symlinks (`filepath.EvalSymlinks`) then checks against `$HOME`, `/`, macOS system dirs, Linux system dirs
- `CheckPathOverlap(dirs []DirMount) error` — resolves symlinks then checks if any two resolved paths have prefix overlap
- `CheckDirtyRepo(path string) (warning string, err error)` — checks for uncommitted git changes

`internal/sandbox/manager.go`:
- `SandboxManager` struct (holds docker.Client, slog.Logger)
- `EnsureSetup(ctx)` — first-run auto-setup, called at the start of `Create`:
  1. Create `~/.yoloai/` directory structure if absent (`sandboxes/`, `profiles/`, `cache/`)
  2. Seed `~/.yoloai/Dockerfile.base` and `entrypoint.sh` from embedded resources (via `SeedResources`) if absent
  3. Build base image if missing (check via Docker image inspect; print "Building base image (first run only, ~2-5 minutes)..." with streamed output)
  4. Print shell completion instructions on first run (detect via absence of `~/.yoloai/config.yaml`)
  5. Write default `config.yaml` if missing

**Verify:** Unit tests for safety checks (dangerous dirs, path overlap, dirty repo). Unit test for `EnsureSetup` with mocked Docker client (verifies directory creation, resource seeding, image check).

### Phase 4b: Sandbox Creation (`yoloai new`)

The core creation workflow — depends on Phase 4a infrastructure.

`internal/sandbox/manager.go` (continued):
- `Create(ctx, CreateOptions) error` — decompose into helper methods (`prepareSandboxState`, `createContainer`, `deliverPrompt`) called sequentially:
  1. Call `EnsureSetup(ctx)` — idempotent first-run auto-setup
  2. Parse workdir arg — extract path, resolve to absolute, validate `:copy`/`:rw`/`:force`
  3. Validate: name non-empty, no duplicate sandbox (unless `--replace`), workdir exists, `--prompt`/`--prompt-file` mutually exclusive, ANTHROPIC_API_KEY set
  4. Run safety checks: dangerous directory detection, path overlap detection (requires valid, existing paths from step 3)
  5. If `--replace`, destroy existing sandbox first
  6. Dirty git repo warning — prompt for confirmation if uncommitted changes detected (skippable with `--yes`)
  7. Create dir structure: `~/.yoloai/sandboxes/<name>/`, `work/`, `agent-state/`
  8. Copy workdir to `work/<encoded-path>/` via `os/exec` `cp -rp` (POSIX-portable; `-a` is GNU-specific and unavailable on macOS). Preserves permissions, timestamps, and symlinks.
  9. Git baseline: if `.git/` exists record HEAD SHA, else `git init + git add -A + git commit`
  10. Always store baseline SHA in meta.json
  11. Write meta.json, prompt.txt (from `--prompt`, `--prompt-file`, or `--prompt -` for stdin), empty log.txt
  12. Resolve model alias: look up `--model` value in agent definition's `ModelAliases` map; if found, substitute the mapped value; if not found, pass through as-is (allows full model names like `claude-sonnet-4-5-20250929`)
  13. Generate `/yoloai/config.json` with host_uid, host_gid, agent_command (built-in flags first — `--dangerously-skip-permissions`, resolved `--model` — then `--` passthrough args appended after), startup_delay, submit_sequence
  14. Create API key temp file via `os.CreateTemp` with `0600` permissions (defer cleanup). Crash-safe: accept that SIGKILL leaves a temp file (same tradeoff as Docker Compose).
  15. If `--no-start`, stop here — print creation output and exit
  16. Create + start Docker container with mounts:
      - `work/<encoded-path>/` → mirrored host path (rw)
      - `agent-state/` → `/home/yoloai/.claude/` (rw)
      - `log.txt` → `/yoloai/log.txt` (rw)
      - `prompt.txt` → `/yoloai/prompt.txt` (ro, if exists)
      - `config.json` → `/yoloai/config.json` (ro)
      - temp key file → `/run/secrets/ANTHROPIC_API_KEY` (ro)
  17. Container config: image `yoloai-base`, name `yoloai-<name>`, `--init`, working dir = mirrored host path, `NetworkMode: "none"` if `--network-none`, `HostConfig.PortBindings` from `--port` flags
  18. Wait for container entrypoint to read secrets (poll for agent process start with 5s timeout), then clean up temp key file
  19. Print context-aware creation output

Wire `yoloai new` — parse `--prompt`/`-p` (including `-` for stdin), `--prompt-file`/`-f` (including `-` for stdin) — mutually exclusive, error if both provided — `--model`/`-m` (resolve built-in aliases via agent definition's `ModelAliases` map: if value found, substitute; if not, pass through as-is), `--agent` (validate: only `claude` for MVP, error on anything else), `--network-none` (sets Docker `NetworkMode: "none"`), `--port` (repeatable string flag, parsed as `host:container` pairs into `HostConfig.PortBindings`), `--replace`, `--no-start`, `--yes`, name, workdir positional args. Collect `--` trailing args via Cobra's `cmd.ArgsLenAtDash()` — returns the index of `--` in positional args (-1 if absent); slice positionals at that index (everything before = name/workdir, everything after = agent passthrough args). Append passthrough args verbatim to agent_command in config.json.

**Creation output (with prompt):**
```
Sandbox fix-bug created
  Agent:    claude
  Workdir:  /home/user/projects/my-app (copy)

Run 'yoloai attach fix-bug' to interact (Ctrl-b d to detach)
    'yoloai diff fix-bug' when done
```

**Creation output (without prompt):**
```
Sandbox explore created
  Agent:    claude
  Workdir:  /home/user/projects/my-app (copy)

Run 'yoloai attach explore' to start working (Ctrl-b d to detach)
```

Profile and network lines omitted when using defaults (base image, unrestricted network). When `--network-none` is used, show `Network: none` (non-default, so not omitted). Strategy line omitted for full copy.

**Creation output (with `--network-none`):**
```
Sandbox offline-test created
  Agent:    claude
  Workdir:  /home/user/projects/my-app (copy)
  Network:  none

Run 'yoloai attach offline-test' to interact (Ctrl-b d to detach)
    'yoloai diff offline-test' when done
```

**Verify:** Integration: `yoloai new test-sandbox /tmp/test-project:copy` creates sandbox, `docker ps` shows `yoloai-test-sandbox`.

### Phase 5: Inspection and Output

**`yoloai attach`:** `os/exec` → `docker exec -it yoloai-<name> tmux attach -t main`. (SDK doesn't handle raw TTY well for interactive tmux — justified exception to "use SDK not CLI".)

**`yoloai show`:** Load `meta.json`, query Docker for container state. Display: name, status (running/stopped/done/failed), agent, prompt (first 200 chars), workdir, directories with access modes, creation time, baseline SHA, container ID. Profile line omitted for MVP (profiles deferred — add back when implemented). Status detection order: check Docker container state FIRST — if stopped/removed, status is "stopped" without querying tmux. Only if the container is running, use `docker exec tmux list-panes -t main -F '#{pane_dead}'` to distinguish running/done/failed. Done (exit 0) vs failed (non-zero) via `pane_dead_status`.

**`yoloai list`:** Scan sandboxes dir, load meta.json for each, query Docker for status. Format table: NAME | STATUS | AGENT | AGE | WORKDIR | CHANGES. PROFILE column omitted for MVP (profiles deferred — add back when implemented). Status uses same done/failed detection as `show`. CHANGES column: run `git status --porcelain` on the host-side work directory for each sandbox — `yes` if any output (catches both tracked modifications and untracked files), `no` if empty, `-` if work dir missing or not a git repo.

**`yoloai log`:** Read `~/.yoloai/sandboxes/<name>/log.txt`. Auto-page through `$PAGER` / `less -R` when stdout is a TTY. Raw output when piped. For real-time following, users can find the log path via `yoloai show` and use `tail -f` directly.

**`yoloai exec`:** `docker exec yoloai-<name> <command>`, with `-i` when stdin is pipe/TTY and `-t` when stdin is TTY.

`internal/cli/pager.go`:
- `RunPager(r io.Reader) error` — pipes content through `$PAGER` / `less -R` when stdout is TTY, otherwise copies to stdout. Used by `diff` and `log`.

**Verify:** Manual test after Phase 4 sandbox creation.

### Phase 6: Diff and Apply

The core differentiator.

**`yoloai diff`:**
- Load meta.json, get baseline SHA
- Run `git add -A` (capture untracked files) then `git diff <baseline_sha>` in `work/<encoded-path>/`
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
- Apply: For original git repos, `git apply` from within the repo. For non-git original dirs, `git apply --unsafe-paths --directory=<path>` to apply without requiring a git repo context. Test this edge case early — `git apply` behavior outside a git repo has subtleties.
- On failure: wrap `git apply` error with context explaining why (e.g., "changes to handler.go conflict with changes already in your working directory — the patch expected line 42 to be 'func foo()' but found 'func bar()'")
- `[-- <path>...]`: apply only changes to specified files/directories (relative to workdir)

**Key subtlety:** Always store baseline SHA in meta.json (Phase 4 step 9). For original git repos, it's the HEAD at copy time. For non-git dirs, it's the SHA of the synthetic initial commit. Diff is always `git diff <baseline_sha>`.

**Verify:** Create sandbox, make a change in the copy manually, `yoloai diff` shows it, `yoloai apply --yes` lands it.

### Phase 7: Lifecycle (stop/start/destroy/reset)

**`yoloai stop`:** Docker SDK `ContainerStop`. Accepts multiple names (e.g., `yoloai stop s1 s2 s3`). `--all` flag stops all running sandboxes. Print confirmation per sandbox.

**`yoloai start`:** Check state — if already running with live agent: no-op; if running but agent exited (tmux pane dead): relaunch agent in existing tmux session (`tmux respawn-pane` or kill dead pane and create new one with agent command); if stopped: `ContainerStart` (entrypoint re-establishes mounts); if container removed: recreate from meta.json (skip copy step, create new credential temp file). Print confirmation.

**`yoloai destroy`:** Accepts multiple names (e.g., `yoloai destroy s1 s2 s3`). `--all` flag. Smart confirmation: only prompt when agent is still running or unapplied changes exist (check via `git status --porcelain` on host-side work directory, consistent with `list` CHANGES detection). `--yes` skips all confirmation. `docker stop` + `docker rm` + `os.RemoveAll` sandbox dir.

**`yoloai reset`:** Full re-copy of workdir from original host directory with git baseline reset. Steps:
1. Stop the container (if running)
2. Delete `work/<encoded-path>/` (the sandbox copy)
3. Re-copy workdir from original host dir via `cp -rp` to `work/<encoded-path>/`
4. Re-create git baseline: if `.git/` exists in copy, record new HEAD SHA; else `git init + git add -A + git commit`
5. Update `baseline_sha` in `meta.json`
6. If `--clean`, also delete and recreate `agent-state/` directory
7. Start container (entrypoint runs as normal)
8. If `prompt.txt` exists and `--no-prompt` not set, wait for agent ready and re-send prompt via tmux

Options: `--no-prompt` (skip re-sending prompt), `--clean` (also wipe `agent-state/` for full reset).

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
- **No Viper for MVP** — CLI flags + hardcoded defaults. Config file parsing comes post-MVP. `EnsureSetup` writes a default `config.yaml` for user reference and future use, but MVP does not read it — all settings come from CLI flags and hardcoded defaults.
- **Entrypoint as shell script** — natural for UID/GID, secrets, tmux. ~50 lines. Go binary would add cross-compilation complexity.
- **`jq` in base image** — entrypoint reads `/yoloai/config.json` via `jq` for all configuration. Simpler and more robust than shell-only JSON parsing.
- **Pager utility** (`internal/cli/pager.go`) — reusable auto-paging for `diff` and `log`. Uses `$PAGER` / `less -R` when stdout is TTY, raw output when piped.
- **Verbose flag** — use Cobra's `CountP` (not `BoolP`) for `--verbose` / `-v` to preserve the stacking contract from CLI-STANDARD.md (`-v` = Debug, `-vv` = reserved). MVP only uses the first level.
- **Reusable confirmation prompt** — shared helper for apply, destroy, and dirty repo warning confirmations.
- **Error types** map to exit codes in root command handler.

## Dependencies

```
github.com/spf13/cobra         # CLI framework
github.com/docker/docker        # Docker SDK (pin to latest v28.x+incompatible; +incompatible is a Go modules artifact, not a stability concern — SDK auto-negotiates API version with older Docker daemons)
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
| `internal/cli/{build,new,attach,show,diff,apply,list,log,exec,stop,start,destroy,reset,completion,version}.go` | Command definitions |
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
3. **Large copies** — `cp -rp` with `node_modules` is slow. Known limitation of full-copy. Overlay (post-MVP) solves this.
4. **Docker SDK version compat** — Pin to latest `github.com/docker/docker` v28.x (the `+incompatible` suffix is expected). SDK auto-negotiates API version with older engines. Test on Docker Desktop for Mac.
