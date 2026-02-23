# Plan: yoloai MVP for Dogfooding

## Context

No code exists yet. All design docs are complete (DESIGN.md, CODING-STANDARD.md, CLI-STANDARD.md). The goal is a working MVP that can dogfood — run `yoloai new fix-build --prompt "fix the build" ~/Projects/yoloai:copy`, have Claude Code work inside a container, then `yoloai diff` / `yoloai apply` to review and land changes.

## Issues to Resolve Before Coding

1. **Go module path** — `github.com/<org>/yoloai` is a placeholder. Need actual org/username.
2. **Node.js version** — Use Node.js 22 LTS (current LTS) via NodeSource APT repo.
3. **tini** — Use `docker run --init` (Docker's built-in tini). Avoids installing in image.
4. **gosu** — Install from GitHub releases (static binary). Standard for Debian images.
5. **Claude ready indicator** — MVP uses fixed 3-second delay (configurable via env var). Polling deferred.
6. **Caret encoding** — MVP only encodes `/` → `^2F` (only filesystem-unsafe char in absolute paths). Simple `strings.ReplaceAll`.

## What's In / What's Deferred

**MVP includes:** `build`, `new` (full-copy only, Claude only), `attach`, `diff`, `apply`, `list`, `stop`, `start`, `destroy`, `log`, credential injection.

**Deferred:** overlay strategy, network isolation/proxy, profiles, Codex agent, `init`, `tail`/`exec`/`restart`/`status`/`version`, `--resume`, config file parsing (Viper), `auto_commit_interval`, custom mount points (`=<path>`), `agent_files`, env var interpolation, context file, aux dirs (`-d`), dirty git warnings, dangerous dir detection, `--model` flag.

## Implementation Phases

### Phase 0: Project Scaffold

Compilable Go project with Cobra CLI that prints help text. No Docker, no functionality.

**Create:**
- `go.mod`, `Makefile` (`build`, `test`, `lint`), `.golangci.yml`
- `cmd/yoloai/main.go` — thin entry point, `signal.NotifyContext`, calls root command
- `internal/cli/root.go` — root Cobra command, `SilenceErrors: true`, `SilenceUsage: true`, custom error→exit code mapping (0/1/2/3)
- `internal/cli/*.go` — stub commands for: `build`, `new`, `attach`, `diff`, `apply`, `list`, `stop`, `start`, `destroy`, `log`

**Verify:** `go build ./...` compiles, `./yoloai --help` shows all commands with correct descriptions.

### Phase 1: Core Domain Types and Path Encoding

Pure Go, fully unit-testable without Docker.

**Create:**

`internal/sandbox/paths.go`:
- `EncodePath(hostPath string) string` — `/` → `^2F`
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
- `ParseDirArg(arg string) (path, mode string, err error)` — splits path from `:copy`/`:rw` suffix

**Verify:** `go test ./internal/sandbox/... ./internal/agent/...` — table-driven tests for encoding round-trips, meta.json serialization, dir arg parsing.

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
- Install: tmux, git, build-essential, python3, curl, ca-certificates, gnupg
- Node.js 22 LTS via NodeSource
- Claude Code: `npm install -g @anthropic-ai/claude-code`
- gosu from GitHub releases
- Create user `yoloai` (UID 1001 placeholder)
- Create `/yoloai/` directory
- Copy entrypoint script

`resources/entrypoint.sh` (~50 lines):
1. Run as root: `usermod -u $HOST_UID yoloai`, `groupmod -g $HOST_GID yoloai` (handle exit code 12)
2. Fix ownership on `/yoloai` and home dir
3. Read `/run/secrets/*`, export filename=content as env vars
4. Drop to yoloai user via gosu
5. Start tmux session `main` with `pipe-pane` to `/yoloai/log.txt`
6. Inside tmux: launch `$YOLOAI_AGENT_CMD`
7. If `/yoloai/prompt.txt` exists: sleep `$YOLOAI_STARTUP_DELAY`, `tmux load-buffer` + `tmux paste-buffer` + `tmux send-keys Enter Enter`
8. Wait for tmux session to end

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

`internal/sandbox/manager.go`:
- `SandboxManager` struct (holds docker.Client, slog.Logger)
- `Create(ctx, CreateOptions) error`:
  1. Parse workdir arg — extract path, resolve to absolute, validate `:copy`
  2. Validate: name non-empty, no duplicate sandbox, workdir exists, ANTHROPIC_API_KEY set
  3. Create dir structure: `~/.yoloai/sandboxes/<name>/`, `work/`, `agent-state/`
  4. `cp -a` workdir to `work/<encoded-path>/`
  5. Git baseline: if `.git/` exists record HEAD SHA, else `git init + git add -A + git commit`
  6. Always store baseline SHA in meta.json (original HEAD or synthetic initial commit SHA)
  7. Write meta.json, prompt.txt (if provided), empty log.txt
  8. Create API key temp file (defer cleanup)
  9. Create + start Docker container with mounts:
     - `work/<encoded-path>/` → mirrored host path (rw)
     - `agent-state/` → `/home/yoloai/.claude/` (rw)
     - `log.txt` → `/yoloai/log.txt` (rw)
     - `prompt.txt` → `/yoloai/prompt.txt` (ro, if exists)
     - temp key file → `/run/secrets/ANTHROPIC_API_KEY` (ro)
  10. Container config: image `yoloai-base`, name `yoloai-<name>`, `--init`, env vars (HOST_UID, HOST_GID, YOLOAI_AGENT_CMD, YOLOAI_STARTUP_DELAY), working dir = mirrored host path
  11. Clean up temp key file

Wire `yoloai new` — parse `--prompt`, name, workdir positional args.

Print after creation:
```
Sandbox 'fix-build' created and running.
  Workdir: /Users/user/Projects/yoloai (copy)
  Agent: claude

Next steps:
  yoloai attach fix-build    # interact with Claude
  yoloai diff fix-build      # review changes
  yoloai apply fix-build     # apply changes to original
```

**Verify:** Unit tests for arg parsing. Integration: `yoloai new test-sandbox /tmp/test-project:copy` creates sandbox, `docker ps` shows `yoloai-test-sandbox`.

### Phase 5: Attach, Log, List

**`yoloai attach`:** `os/exec` → `docker exec -it yoloai-<name> tmux attach -t main`. (SDK doesn't handle raw TTY well for interactive tmux — this is the justified exception to "use SDK not CLI".)

**`yoloai log`:** Read and print `~/.yoloai/sandboxes/<name>/log.txt`. Pipe through pager when stdout is TTY.

**`yoloai list`:** Scan sandboxes dir, load meta.json for each, query Docker for status. Format table: NAME | STATUS | AGENT | AGE | WORKDIR.

**Verify:** Manual test after Phase 4 sandbox creation.

### Phase 6: Diff and Apply

The core differentiator.

**`yoloai diff`:**
- Load meta.json, get baseline SHA
- In `work/<encoded-path>/`: `git diff <baseline_sha>`
- If original had no `.git/`, exclude synthetic git dir: `git diff <baseline_sha> -- ':!.git'`
- Pipe through pager when stdout is TTY

**`yoloai apply`:**
- Generate patch from diff (same baseline logic as above)
- If empty: "No changes to apply", exit
- Show `git diff --stat <baseline_sha>` summary
- Dry-run: `git apply --check` on original host dir
- Prompt: `Apply these changes to /path/to/original? [y/N]` (skip with `--yes`)
- Apply: `git apply` on original host dir

**Key subtlety:** Always store baseline SHA in meta.json (Phase 4 step 6). For original git repos, it's the HEAD at copy time. For non-git dirs, it's the SHA of the synthetic initial commit. Diff is always `git diff <baseline_sha>`.

**Verify:** Create sandbox, make a change in the copy manually, `yoloai diff` shows it, `yoloai apply --yes` lands it.

### Phase 7: Lifecycle (stop/start/destroy)

**`yoloai stop`:** Docker SDK `ContainerStop`. Print confirmation.

**`yoloai start`:** Check state — if running: no-op; if stopped: `ContainerStart`; if container removed: recreate from meta.json. Print confirmation.

**`yoloai destroy`:** Prompt if running (skip with `--yes`). `ContainerStop` + `ContainerRemove` + `os.RemoveAll` sandbox dir.

**Verify:** Full lifecycle: new → stop → start → destroy.

### Phase 8: Integration Test and Dogfood

`internal/sandbox/integration_test.go` (build tag `//go:build integration`):
1. Create temp dir with simple Go project
2. Build base image (or verify exists)
3. Create sandbox with prompt
4. Wait for agent activity (poll log.txt)
5. Diff, apply, destroy
6. Verify original was modified

Manual dogfood: `yoloai new fix-something --prompt "fix the build" ~/Projects/yoloai:copy`

## Architecture Decisions

- **`SandboxManager`** is the central orchestrator. CLI commands are thin (parse args → call manager method).
- **git via `os/exec`** — simpler than go-git for our needs (diff, apply, init, add, commit, rev-parse).
- **`docker exec` for attach via `os/exec`** — justified exception to "use SDK" rule for interactive TTY.
- **No Viper for MVP** — CLI flags + hardcoded defaults. Config file parsing comes post-MVP.
- **Entrypoint as shell script** — natural for UID/GID, secrets, tmux. ~50 lines. Go binary would add cross-compilation complexity.
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
| `internal/cli/{build,new,attach,diff,apply,list,stop,start,destroy,log}.go` | Command definitions |
| `internal/sandbox/paths.go` | Caret encoding, dir layout |
| `internal/sandbox/meta.go` | meta.json types and I/O |
| `internal/sandbox/parse.go` | Dir arg parsing |
| `internal/sandbox/errors.go` | Error types for exit codes |
| `internal/sandbox/manager.go` | All sandbox operations |
| `internal/docker/client.go` | Docker SDK wrapper interface |
| `internal/docker/build.go` | Image build logic |
| `internal/agent/agent.go` | Agent definitions (Claude) |
| `resources/Dockerfile.base` | Base Docker image |
| `resources/entrypoint.sh` | Container entrypoint |
| Test files | `*_test.go` alongside each package |

## Risks

1. **Entrypoint fragility** — UID/GID + secrets + tmux + prompt delivery in one script. Mitigate: test manually early (Phase 3).
2. **tmux timing** — 3s delay may not suffice on slow machines. Mitigate: configurable via `YOLOAI_STARTUP_DELAY`.
3. **Large copies** — `cp -a` with `node_modules` is slow. Known limitation of full-copy. Overlay (post-MVP) solves this.
4. **Docker SDK version compat** — Pin to known-good version, test on Docker Desktop for Mac.
