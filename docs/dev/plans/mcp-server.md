# MCP Server Implementation Plan

Implement `yoloai system mcp` — a stdio MCP server exposing yoloAI sandbox
operations as tools for Claude Desktop, VS Code Copilot, or any outer agent
driving the two-layer agentic workflow.

## Design decisions (final)

- **Single binary**: `yoloai system mcp` subcommand, not a separate binary.
- **Transport**: stdio only (Claude Desktop standard). SSE is future work.
- **No `sandbox_apply`**: intentionally absent. The outer agent applies changes
  using its own file tools and permission system. The diff is the handoff artifact.
- **Refinement**: two tools covering all three scenarios:
  - `sandbox_input` — tmux send-keys at any time. Interrupt if agent is running;
    follow-up if idle at prompt. Outer agent decides when to call based on status.
  - `sandbox_reset` — full reset for poisoned context. Optional new prompt.
- **Hook detection**: Claude-specific for now. `Idle.Hook = true` is already set
  on the Claude agent definition. Pluggable interface deferred until a second
  agent warrants it.
- **CLAUDE.md seeding**: always-on. File Q&A protocol instructions added to every
  Claude sandbox via the existing `ContextFile` mechanism.

## Tool surface (12 tools)

### Lifecycle
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_create` | `name, workdir, prompt, agent?, model?, profile?` | `Manager.EnsureSetup` + `Manager.Create` |
| `sandbox_status` | `name` | `Manager.Inspect` |
| `sandbox_list` | — | `Manager.List` |
| `sandbox_destroy` | `name, force?` | `Manager.Destroy` |

`sandbox_create` returns immediately. Caller polls `sandbox_status`. Do not
block — MCP clients time out on long tool calls.

### Observation
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_diff` | `name, stat?` | `GenerateDiffStat` (stat=true) or `GenerateMultiDiff` (stat=false) |
| `sandbox_diff_file` | `name, path` | `GenerateDiff` with `Paths: []string{path}` |
| `sandbox_log` | `name, lines?` | read+tail `LogFilePath(name)` (default 100 lines) |

`sandbox_diff` with `stat=true` is cheap — call it first. Fetch full diff or
`sandbox_diff_file` for specific paths only when needed (context window pressure).

### Refinement
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_input` | `name, text` | `Manager.SendInput` (new) |
| `sandbox_reset` | `name, prompt?` | `Manager.Reset` |

### Files (Q&A channel)
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_files_list` | `name` | `os.ReadDir(FilesDir(name))` |
| `sandbox_files_read` | `name, filename` | `os.ReadFile(filepath.Join(FilesDir(name), filename))` |
| `sandbox_files_write` | `name, filename, content` | `os.WriteFile(filepath.Join(FilesDir(name), filename))` |

File operations are scoped to `FilesDir(name)` only — no arbitrary container
filesystem access.

## Implementation steps

### Step 1: `Manager.SendInput`

New method in `sandbox/manager.go`:

```go
// SendInput sends text to the sandbox agent's terminal via tmux send-keys.
// If the agent is running, this interrupts it mid-task. If the agent is idle
// at its prompt, this sends a follow-up message. The caller should check
// Manager.Status before calling to know which case applies.
func (m *Manager) SendInput(ctx context.Context, name string, text string) error {
    containerName := InstanceName(name)
    _, err := m.runtime.Exec(ctx, containerName,
        []string{"tmux", "send-keys", "-t", "main", text, "Enter"},
        "yoloai",
    )
    if err != nil {
        return fmt.Errorf("send input to sandbox %q: %w", name, err)
    }
    return nil
}
```

Tmux session is `"main"`, user is `"yoloai"` — confirmed from `attach.go` and
`relaunchAgentWithResume` in `lifecycle.go`.

### Step 2: Hook config injection for Claude

**Goal**: when the inner Claude Code finishes a turn or goes idle, it writes to
`agent-status.json` so `sandbox_status` can return a meaningful `AgentStatus`.

`Idle.Hook = true` is already set on the Claude agent definition — this is the
signal that hook wiring should be applied.

**Hook config to inject** (into the sandboxed Claude's `settings.json`):

```json
{
  "hooks": {
    "Stop": [{
      "hooks": [{
        "type": "command",
        "command": "printf '{\"agent_status\":\"idle\"}' > /yoloai/agent-status.json"
      }]
    }],
    "Notification": [{
      "matcher": "idle_prompt",
      "hooks": [{
        "type": "command",
        "command": "printf '{\"agent_status\":\"waiting_for_input\"}' > /yoloai/agent-status.json"
      }]
    }]
  }
}
```

**Injection mechanism**: Claude Code reads hooks from `~/.claude/settings.json`
(already seeded by yoloAI from the host). In `sandbox/agent_files.go` or the
create path, after seed files are copied into the sandbox's agent-runtime
directory, check if `agentDef.Idle.Hook` is true and merge the hook config into
the sandboxed copy of `settings.json`.

Steps:
1. Find where `copySeedFiles` writes the agent state files to the host-side
   sandbox directory (likely under `AgentRuntimePath(name)`).
2. After copy, if `agentDef.Idle.Hook`, read the sandboxed `settings.json`,
   merge in the hook config JSON (add `hooks` key; merge if key already exists),
   write back.
3. If `settings.json` wasn't seeded (host file didn't exist), create it with
   just the hook config.

**Note**: `idle_prompt` is known to be unreliable (see orchestration research).
The `Stop` hook is the reliable signal. `idle_prompt` is included cheaply but
expect to revisit. If it proves too noisy, remove it; `DetectStatus` already
falls back to tmux heuristics for running/done distinction.

### Step 3: CLAUDE.md seeding update

The Claude agent definition already has `ContextFile: "CLAUDE.md"`. Find where
this file is generated/written (likely `sandbox/context.go` or `sandbox/create.go`
around the `GenerateContext`/`WriteContextFiles` calls) and append:

```markdown
## yoloAI File Exchange Protocol

You are running inside a yoloAI sandbox. A file exchange directory is
available at `/yoloai/files/` — readable and writable from both inside
and outside the sandbox.

**When you need to ask a question or need input to continue:**

1. Write your question to `/yoloai/files/question.json`:
   ```json
   {"question": "your question here", "context": "optional context"}
   ```
2. Poll `/yoloai/files/answer.json` every 5 seconds until it appears.
3. Read the answer and continue your task.

Do not make assumptions about blocking decisions. Write the question file
and wait. The question will be seen and answered by an external agent or user.
```

### Step 4: MCP server package

New package: `internal/mcp/`

**`internal/mcp/server.go`** — server construction and tool registration:

```go
package mcp

import (
    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
    "github.com/kstenerud/yoloai/sandbox"
)

type Server struct {
    mgr *sandbox.Manager
    srv *server.MCPServer
}

func New(mgr *sandbox.Manager) *Server {
    s := &Server{mgr: mgr}
    s.srv = server.NewMCPServer(
        "yoloai",
        "1.0.0",
        server.WithToolCapabilities(true),
    )
    s.registerTools()
    return s
}

func (s *Server) ServeStdio(ctx context.Context) error {
    return server.ServeStdio(s.srv)
}
```

**`internal/mcp/tools.go`** — one handler per tool.

Tool description conventions:
- `sandbox_create`: "Returns immediately. Poll sandbox_status until status is done or failed."
- `sandbox_status`: "Poll this after sandbox_create. agent_status: active=working, idle=waiting at prompt, waiting_for_input=asked a question (check sandbox_files_read for question.json), done=finished, failed=error."
- `sandbox_diff`: "Call with stat=true first for a summary. Use sandbox_diff_file to fetch specific files when the full diff is too large."
- `sandbox_input`: "Check sandbox_status first. If agent_status is 'idle', this continues the conversation. If 'active', this interrupts the current task — use carefully."
- `sandbox_reset`: "Use when the agent has gone off track and the conversation context is poisoned. Starts fresh. Optionally supply a revised prompt."
- `sandbox_files_write`: "Write answer.json here to respond to a question.json the agent has created. Format: {\"answer\": \"your answer\"}."

Error format: return human-readable strings prefixed with `[ERROR]` so the outer
agent can reason about failures without structured error parsing:
```
"[ERROR] sandbox 'mybox' not found"
"[ERROR] no changes to diff"
```

**Dependency**: `github.com/mark3labs/mcp-go` — add to `go.mod`.
Verify this is still the right choice vs the official Anthropic Go MCP SDK at
implementation time.

### Step 5: `yoloai system mcp` subcommand

New file: `internal/cli/system_mcp.go`

```go
func newMCPCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "mcp",
        Short: "Start the yoloAI MCP server (stdio)",
        Long: `Start the yoloAI MCP server on stdin/stdout.

Add to ~/.claude.json to use with Claude Desktop:

  {
    "mcpServers": {
      "yoloai": {
        "command": "yoloai",
        "args": ["system", "mcp"]
      }
    }
  }`,
        Args: cobra.NoArgs,
        RunE: runMCP,
    }
}

func runMCP(cmd *cobra.Command, args []string) error {
    backend := resolveBackendFromConfig()
    return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
        srv := mcp.New(mgr)
        return srv.ServeStdio(ctx)
    })
}
```

Register under the `system` subcommand in `internal/cli/system.go`.

## Files to create

- `internal/mcp/server.go` — server struct, `New`, `ServeStdio`
- `internal/mcp/tools.go` — all 12 tool handlers
- `internal/cli/system_mcp.go` — `yoloai system mcp` subcommand

## Files to modify

- `go.mod` / `go.sum` — add `github.com/mark3labs/mcp-go`
- `sandbox/manager.go` — add `SendInput` method
- `sandbox/agent_files.go` (or create path) — hook config merge for `Idle.Hook` agents
- `sandbox/context.go` (or wherever CLAUDE.md is written) — append Q&A protocol
- `internal/cli/system.go` — register `mcp` subcommand

## Implementation order

Dependencies flow top to bottom:

1. `Manager.SendInput` — no deps, unblocks `sandbox_input` tool
2. Hook config injection — find the seed file copy path, add merge logic
3. CLAUDE.md update — find context file write path, append Q&A text
4. `internal/mcp/` package — depends on 1; implements all 12 tools
5. `yoloai system mcp` — depends on 4
6. `go mod tidy` + `make check`

## Open questions to resolve during implementation

1. **Hook config merge location**: Find exactly where `copySeedFiles` writes the
   sandboxed `settings.json` on the host side and confirm it's writable before
   container start. If settings.json isn't seeded (host file absent), create it.

2. **`idle_prompt` reliability**: If the `Notification/idle_prompt` hook proves
   too noisy or never fires, drop it. `Stop` alone is enough for the common case.
   The outer agent can use `sandbox_log` + its own Claude judgment to detect
   `waiting_for_input` as a fallback.

3. **mcp-go vs official SDK**: Check whether Anthropic has published a stable
   official Go MCP SDK. If yes and it's stable, prefer it. Otherwise use
   `github.com/mark3labs/mcp-go`.

4. **`sandbox_log` tail implementation**: `LogFilePath(name)` returns the log
   file path. Tail last N lines by reading the file and taking the last N
   newline-separated chunks. For large files, seek from end.

## Outer agent workflow (reference)

The MCP tools support this loop:

```
sandbox_create(name, workdir, prompt)
loop:
  sandbox_status(name)
  → waiting_for_input:
      sandbox_files_read(name, "question.json")  → read question
      [reason or surface to user]
      sandbox_files_write(name, "answer.json", ...)
  → active: continue polling
  → done/failed: break
sandbox_diff(name, stat=true)         → summary
sandbox_diff_file(name, path)         → details for interesting files
[surface to user, get approval]
[outer agent applies using its own file tools — NOT via yoloai MCP]
sandbox_destroy(name)
```

Refinement loop (before destroy):
```
sandbox_diff(name) → not right
sandbox_input(name, "the email verification step got removed, add it back")
→ back to polling loop
```

Poisoned context remedy:
```
sandbox_reset(name, "revised prompt with clearer constraints")
→ back to polling loop
```
