# MCP Server Implementation Plan

Two MCP server personalities exposed as subcommands:

- **`yoloai system mcp`** — orchestration server. Exposes sandbox lifecycle,
  observation, refinement, and file Q&A tools. No `sandbox_apply`; the outer
  agent applies changes using its own file tools and permission system.
- **`yoloai system mcp-proxy`** — proxy server. Proxies any MCP server running
  inside a sandbox through stdio, injecting `sandbox_diff` and `sandbox_apply`
  into the tool surface. `sandbox_apply` is gated by MCP elicitation.

Both are single-binary subcommands, stdio transport only. SSE is future work.

---

## Personality 1: Orchestration Server (`yoloai system mcp`)

### Design decisions

- **No `sandbox_apply`**: intentionally absent. The outer agent applies changes
  using its own file tools and permission system. The diff is the handoff artifact.
- **Refinement**: three tools covering all scenarios:
  - `sandbox_input` — tmux send-keys at any time. Interrupts if agent is running;
    sends follow-up if idle at prompt. Outer agent checks status before calling.
  - `sandbox_reset` — full reset for poisoned context. Accepts optional new prompt
    (handler writes prompt.txt before resetting — see implementation note).
- **Hook detection**: Claude-specific for now. `Idle.Hook = true` is already set
  on the Claude agent definition. Pluggable interface deferred until a second
  agent warrants it.
- **CLAUDE.md seeding**: always-on. File Q&A protocol instructions added to every
  Claude sandbox via the existing `ContextFile` mechanism.

### Tool surface (12 tools)

#### Lifecycle
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_create` | `name, workdir, prompt, agent?, model?, profile?` | `Manager.EnsureSetup` + `Manager.Create` |
| `sandbox_status` | `name` | `Manager.Inspect` |
| `sandbox_list` | — | `Manager.List` |
| `sandbox_destroy` | `name, force?` | `Manager.Destroy` |

`sandbox_create` returns immediately. Poll `sandbox_status` every 5 seconds.
Do not block — MCP clients time out on long tool calls.

`name` is required. Recommended convention for generated names: `<project>-<timestamp>`.

#### Observation
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_diff` | `name, stat?` | `GenerateDiffStat` (stat=true) or `GenerateMultiDiff` (stat=false) |
| `sandbox_diff_file` | `name, path` | `GenerateDiff` with `Paths: []string{path}` |
| `sandbox_log` | `name, lines?` | read+tail `LogFilePath(name)` (default 100 lines) |

`sandbox_diff` with `stat=true` is cheap — call it first. Fetch full diff or
`sandbox_diff_file` for specific paths only when needed (context window pressure).

**Note**: overlay-mode directories require the container to be running for diff
(overlayfs upper layer is inside the container). If the container is stopped,
`sandbox_diff` returns a placeholder noting this. Copy-mode (default) works
anytime.

#### Refinement
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_input` | `name, text` | `Manager.SendInput` (new) |
| `sandbox_reset` | `name, prompt?` | write prompt.txt + `Manager.Reset` |

**`sandbox_reset` implementation note**: `ResetOptions` has no `Prompt` field.
Reset always re-sends the existing `prompt.txt`. To reset with a new prompt,
the handler must write the new prompt text to `<SandboxDir(name)>/prompt.txt`
before calling `Reset(ctx, ResetOptions{Name: name, Restart: true})`.
If `prompt` param is absent, leave prompt.txt unchanged.

#### Files (Q&A channel)
| Tool | Params | Backed by |
|------|--------|-----------|
| `sandbox_files_list` | `name` | `os.ReadDir(FilesDir(name))` |
| `sandbox_files_read` | `name, filename` | `os.ReadFile(filepath.Join(FilesDir(name), filename))` |
| `sandbox_files_write` | `name, filename, content` | `os.WriteFile(filepath.Join(FilesDir(name), filename))` |

File operations are scoped to `FilesDir(name)` only. **`sandbox_files_write`
must validate that `filename` contains no path separators or `..` components**
to prevent path traversal. Return `[ERROR]` if validation fails.

### Outer agent workflow (reference)

```
sandbox_create(name, workdir, prompt)
loop (poll every 5s):
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

---

## Personality 2: Proxy Server (`yoloai system mcp-proxy`)

### Design decisions

- **What it does**: runs any MCP-capable server binary inside a sandbox container
  via `docker exec`, forwarding stdio. The outer agent connects as if talking
  directly to the inner server. yoloAI injects two additional tools.
- **Injected tools**: `sandbox_diff` (read-only, always safe) and `sandbox_apply`
  (write, guarded by MCP elicitation). All other tools come from the inner server.
- **`sandbox_apply` guard**: before applying, the proxy server sends an MCP
  elicitation request to the client asking for explicit human approval. If the
  client does not support elicitation, the tool returns an error explaining that
  apply requires human approval and instructing the user to run `yoloai apply
  <name>` manually. No silent apply.
- **Sandbox lifecycle**: proxy mode requires the sandbox to already exist. Use
  `sandbox_create` (orchestration server) or `yoloai new` CLI to create it first.
  This keeps the proxy focused on proxying rather than lifecycle management.
- **Reconnection after reset**: `sandbox_reset` in proxy mode terminates the inner
  MCP process. The proxy must restart the inner process after reset completes and
  re-establish the MCP connection. The outer agent sees a transparent reconnect.

### CLI

```
yoloai system mcp-proxy <name> -- <inner-command> [args...]
```

`<name>` is the sandbox to proxy into. `<inner-command>` is the MCP server
command to run inside the sandbox (e.g. `npx -y @modelcontextprotocol/server-filesystem /workspace`).

### Tool injection mechanism

The proxy intercepts `tools/list` responses from the inner server and appends:

```json
{
  "name": "sandbox_diff",
  "description": "Show a diff of all changes made in the sandbox. Call with stat=true for a cheap summary first.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "stat": {"type": "boolean", "description": "Return stat summary only (default false)"}
    }
  }
},
{
  "name": "sandbox_apply",
  "description": "Apply sandbox changes to the host working directory. Requires explicit human approval via elicitation. Use sandbox_diff first to review changes.",
  "inputSchema": {"type": "object", "properties": {}}
}
```

Tool calls for `sandbox_diff` and `sandbox_apply` are intercepted and handled
locally; all other tool calls are forwarded to the inner server.

### MCP elicitation for `sandbox_apply`

MCP elicitation (spec: `elicitation/create` request from server to client) lets
the server request a human decision before proceeding. Claude Desktop supports it.

**Happy path** (client supports elicitation):
1. Handler calls `elicitation/create` with a prompt showing the diff stat and
   asking "Apply these changes to the host directory?"
2. If user approves → call `sandbox.ApplyAll`, return result.
3. If user cancels → return `[CANCELLED] Apply cancelled by user.`

**Degraded path** (client returns `method not found` or no response):
- Return: `[ERROR] sandbox_apply requires human approval. Your MCP client does not
  support elicitation. Run 'yoloai apply <name>' from the terminal to apply changes.`

**mcp-go elicitation support**: verify at implementation time whether
`mark3labs/mcp-go` exposes an elicitation API. If not, fall back to the
degraded path unconditionally and add a TODO for when elicitation lands.
Alternatively, check the official Anthropic Go MCP SDK for elicitation support.

### Proxy reconnection after sandbox_reset

When the outer agent calls `sandbox_reset` through the proxy:
1. Proxy tears down the inner MCP stdio connection.
2. Calls `Manager.Reset(...)` (optionally writing new prompt.txt first).
3. Waits for container to be ready (poll `Manager.Inspect` until `StatusRunning`).
4. Re-launches inner MCP process via `docker exec`.
5. Returns success. Outer agent continues using the same MCP connection.

---

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
this file is generated/written (`sandbox/context.go`, `GenerateContext` /
`WriteContextFiles`) and append:

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

### Step 4: MCP server packages

**Package naming**: use `internal/mcpsrv/` (not `internal/mcp/`) to avoid
collision with `github.com/mark3labs/mcp-go/mcp` import path.

#### `internal/mcpsrv/server.go` — orchestration server

```go
package mcpsrv

import (
    "context"
    mcpgo "github.com/mark3labs/mcp-go/mcp"
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

#### `internal/mcpsrv/tools.go` — one handler per tool

Tool description conventions:
- `sandbox_create`: "Returns immediately. Poll sandbox_status every 5s until agent_status is done or failed."
- `sandbox_status`: "Poll this after sandbox_create. agent_status: active=working, idle=waiting at prompt, waiting_for_input=asked a question (check sandbox_files_read for question.json), done=finished, failed=error."
- `sandbox_diff`: "Call with stat=true first for a summary. Use sandbox_diff_file to fetch specific files when the full diff is too large. Requires container running for overlay-mode directories."
- `sandbox_input`: "Check sandbox_status first. If agent_status is 'idle', this continues the conversation. If 'active', this interrupts the current task — use carefully."
- `sandbox_reset`: "Use when the agent has gone off track and the conversation context is poisoned. Starts fresh. Optionally supply a revised prompt."
- `sandbox_files_write`: "Write answer.json here to respond to a question.json the agent has created. Format: {\"answer\": \"your answer\"}. filename must be a plain filename with no path separators."

Error format: return human-readable strings prefixed with `[ERROR]` so the outer
agent can reason about failures without structured error parsing:
```
"[ERROR] sandbox 'mybox' not found"
"[ERROR] no changes to diff"
"[ERROR] filename must not contain path separators"
```

#### `internal/mcpsrv/proxy.go` — proxy server

```go
type ProxyServer struct {
    mgr          *sandbox.Manager
    sandboxName  string
    innerCmd     []string   // command to exec inside container
    innerProcess *exec.Cmd  // live inner MCP process
    srv          *server.MCPServer
}

func NewProxy(mgr *sandbox.Manager, sandboxName string, innerCmd []string) *ProxyServer
func (p *ProxyServer) ServeStdio(ctx context.Context) error
```

Proxy logic:
- On `tools/list`: forward to inner, append injected tools, return merged list.
- On `tools/call` for `sandbox_diff`/`sandbox_apply`: handle locally.
- On `tools/call` for anything else: forward bytes to inner process stdin, read
  response from inner stdout, return.
- On inner process exit: return error to outer agent.

**Dependency**: `github.com/mark3labs/mcp-go` — add to `go.mod`.
Verify this is still the right choice vs the official Anthropic Go MCP SDK at
implementation time. Check elicitation support in whichever SDK is chosen.

### Step 5: CLI subcommands

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

func newMCPProxyCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "mcp-proxy <name> -- <command> [args...]",
        Short: "Proxy an MCP server running inside a sandbox (stdio)",
        Long: `Run an MCP server inside a sandbox and proxy its stdio to the caller.
Injects sandbox_diff and sandbox_apply tools. sandbox_apply requires human
approval via MCP elicitation.

The sandbox must already exist (use 'yoloai new' or sandbox_create first).

Example:
  yoloai system mcp-proxy mybox -- npx -y @modelcontextprotocol/server-filesystem /workspace`,
        Args: cobra.MinimumNArgs(1),
        RunE: runMCPProxy,
    }
}
```

Register both under the `system` subcommand in `internal/cli/system.go`.

## Files to create

- `internal/mcpsrv/server.go` — orchestration server struct, `New`, `ServeStdio`
- `internal/mcpsrv/tools.go` — all 12 orchestration tool handlers
- `internal/mcpsrv/proxy.go` — proxy server, tool injection, elicitation
- `internal/cli/system_mcp.go` — `yoloai system mcp` and `yoloai system mcp-proxy` subcommands

## Files to modify

- `go.mod` / `go.sum` — add MCP Go SDK
- `sandbox/manager.go` — add `SendInput` method
- `sandbox/agent_files.go` (or create path) — hook config merge for `Idle.Hook` agents
- `sandbox/context.go` — append Q&A protocol to CLAUDE.md generation
- `internal/cli/system.go` — register `mcp` and `mcp-proxy` subcommands

## Implementation order

Dependencies flow top to bottom:

1. `Manager.SendInput` — no deps, unblocks `sandbox_input` tool
2. Hook config injection — find seed file copy path, add merge logic
3. CLAUDE.md update — find context file write path, append Q&A text
4. `internal/mcpsrv/` package — depends on 1; orchestration server first
5. `internal/mcpsrv/proxy.go` — proxy server, depends on 4
6. `yoloai system mcp` + `yoloai system mcp-proxy` CLI — depends on 4+5
7. `go mod tidy` + `make check`

## Open questions to resolve during implementation

1. **Hook config merge location**: Find exactly where `copySeedFiles` writes the
   sandboxed `settings.json` on the host side and confirm it's writable before
   container start. If settings.json isn't seeded (host file absent), create it.

2. **`idle_prompt` reliability**: If the `Notification/idle_prompt` hook proves
   too noisy or never fires, drop it. `Stop` alone is enough for the common case.

3. **MCP SDK choice**: Check whether Anthropic has published a stable official
   Go MCP SDK. If yes and stable, prefer it. Otherwise use
   `github.com/mark3labs/mcp-go`. **Also verify elicitation API availability**
   in whichever SDK is chosen before writing proxy code.

4. **`sandbox_log` tail implementation**: `LogFilePath(name)` returns the log
   file path. Tail last N lines by reading the file and taking the last N
   newline-separated chunks. For large files, seek from end.

5. **Proxy stdio forwarding**: The proxy must correctly multiplex JSON-RPC 2.0
   messages (newline-delimited) between outer agent ↔ proxy ↔ inner process.
   Use a goroutine pair: one forwarding outer→inner, one forwarding inner→outer,
   with the proxy intercepting `tools/list` and `tools/call` messages before
   forwarding. Consider whether to parse full JSON or do string-match interception
   (full parse is safer).

6. **Proxy reconnection error window**: Between `sandbox_reset` completing and
   the inner MCP process restarting, any tool call from the outer agent will fail.
   Document this in the tool description: "Subsequent tool calls may fail briefly
   while the inner server restarts."
