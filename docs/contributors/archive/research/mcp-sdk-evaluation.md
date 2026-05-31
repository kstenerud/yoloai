# MCP SDK Evaluation: Official vs Community Go SDK

Evaluated: 2026-05-23

## Recommendation

**Stay with `github.com/mark3labs/mcp-go` (v0.54.0) for now; switch to `github.com/modelcontextprotocol/go-sdk` after it reaches v1.1+ and yoloai needs elicitation.**

The official SDK (`modelcontextprotocol/go-sdk`) is the clear long-term winner — it is co-maintained by Google and the MCP org, ships a genuinely Go-idiomatic typed handler API, and already implements elicitation (which `mcp-go` does not). However, at v1.6.1 it still carries open reliability issues (#958: unrecovered goroutine panics can crash the host), its tool-definition API uses struct tags rather than the functional-options style yoloai already has, and the migration cost is non-trivial. `mcp-go` v0.54.0 is stable enough for yoloai's actual current use (stdio + simple text results), is well-understood by the team, and is under active development targeting the same spec version (2025-11-25). Migrate when: (a) yoloai needs elicitation or (b) the official SDK closes the goroutine-panic issue and the API settles.

---

## Status of Each SDK

### `github.com/modelcontextprotocol/go-sdk` (official)

| Property | Value |
|---|---|
| Import path | `github.com/modelcontextprotocol/go-sdk` (primary API in subpackage `mcp`) |
| pkg.go.dev | Published at `pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp` |
| Latest version | v1.6.1 (2026-05-22) |
| Stability | v1.x — major version declared stable |
| Stars | 4.6k |
| Forks | 435 |
| Releases | 25 tagged releases |
| Open issues | 34 |
| Maintainers | MCP organization + Google (README: "Maintained in collaboration with Google") |
| MCP spec version | 2025-11-25 (full) since v1.4.0 |
| Elicitation | Yes — `ServerSession.Elicit()`, `ClientOptions.ElicitationHandler`, `ElicitParams`/`ElicitResult` types, `ElicitationCapabilities`. Available since v1.3.0. |

**Elicitation API (verified from pkg.go.dev):**
```go
// Server calls elicit on the connected client:
func (ss *ServerSession) Elicit(ctx context.Context, params *ElicitParams) (*ElicitResult, error)

// Client provides handler in options:
type ClientOptions struct {
    ElicitationHandler func(context.Context, *ElicitRequest) (*ElicitResult, error)
    // ...
}
```

**Known open issues:**
- #958 (open): Library goroutines lack panic recovery — 9 goroutines can crash host process
- #961 (open): Duplicate initialize with changed parameters can overwrite session state
- #966 (open): SEP-2575 statelessness not yet implemented

**Release cadence:** Active. v1.4.0 (Feb 2025) → v1.5.0 (Apr 2025) → v1.6.0 (May 2025) → v1.6.1 (May 22 2025). Roughly monthly major releases.

---

### `github.com/mark3labs/mcp-go` (community)

| Property | Value |
|---|---|
| Import path | `github.com/mark3labs/mcp-go` (subpackages: `mcp`, `server`) |
| pkg.go.dev | Published at `pkg.go.dev/github.com/mark3labs/mcp-go` |
| Latest version | v0.54.0 (2026-05-13) |
| Stability | v0.x — explicitly unstable |
| Stars | 8.7k |
| Open issues | 7 |
| Open PRs | 12 |
| MCP spec version | 2025-11-25 (with backward compat for 2025-06-18, 2025-03-26, 2024-11-05) |
| Elicitation | **No.** Code search `repo:mark3labs/mcp-go elicitation` returns zero files. Issue #817 references elicitation in error text (misreported as "sampling error") in a bug from April 2026, but there is no elicitation implementation. |

**Release cadence:** Very active. v0.51.0 → v0.54.0 across a few weeks in April-May 2026. Total 551 commits.

**Community signal:** 8.7k stars vs 4.6k for the official SDK, suggesting it became the de facto Go MCP SDK before the official one existed. The low open issue count (7) vs the official (34) could mean either better quality or smaller usage surface — given the star count, likely the former.

---

## API Surface Comparison

### Tool definition

**mcp-go (current yoloai style):** Functional options pattern.
```go
tool := mcp.NewTool("sandbox_create",
    mcp.WithDescription("..."),
    mcp.WithString("name", mcp.Required(), mcp.Description("...")),
    mcp.WithBoolean("force", mcp.Description("...")),
)
```
Handler receives `mcp.CallToolRequest` and pulls args by name:
```go
func handler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    name := req.GetString("name", "")
    force := req.GetBool("force", false)
    // ...
    return mcp.NewToolResultText("ok"), nil
}
```
No typed input struct — args accessed by key at runtime.

**go-sdk (official):** Struct tags + generics pattern.
```go
type SandboxCreateInput struct {
    Name    string `json:"name"    jsonschema:"Unique sandbox name"`
    Workdir string `json:"workdir" jsonschema:"Absolute host working directory path"`
    Force   bool   `json:"force"   jsonschema:"Force destroy even if unapplied changes"`
}

mcp.AddTool(server, &mcp.Tool{Name: "sandbox_create", Description: "..."}, 
    func(ctx context.Context, req *mcp.CallToolRequest, args SandboxCreateInput) (*mcp.CallToolResult, any, error) {
        // args.Name, args.Force are typed — no key lookup
        return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
    })
```
Schema is inferred from the struct at registration time. Input is validated before the handler is called.

**Assessment:** The official SDK's typed handler is more Go-idiomatic (compiler-checked args, no runtime key lookup) and aligns with how Go generics should be used. The mcp-go functional options pattern is familiar to the team and produces readable tool definitions, but the handler arg extraction is stringly typed and error-prone.

### Streaming

**mcp-go:** Supports stdio, SSE, and streamable HTTP transports. Async task-augmented tools via `CreateTaskResult`. Channels for notifications (`chan mcp.JSONRPCNotification`).

**go-sdk:** Supports stdio (`StdioTransport`), subprocess (`CommandTransport`), in-memory, SSE (`SSEHandler`), streamable HTTP (`StreamableHTTPHandler`). Tool results are returned synchronously (`*CallToolResult`) — no streaming within a single tool call. Progress is sent via `ss.NotifyProgress()` on the `ServerSession`.

Neither SDK has a streaming tool-result API (i.e., you cannot stream partial results from a tool handler). Both deliver full results as a single response. This means yoloai's `Client.Attach()` / `Exec()` streaming shapes are **not constrained by MCP tool result semantics** — the stream lives inside the tool handler (polling or progress notifications), not in the return value.

### Go idiom quality

**mcp-go:** Feels like a Go-native design. Functional options, context threading, interface-based session, middleware composition. Minor rough edge: handler arg access is stringly typed.

**go-sdk:** More Go-idiomatic in its typed handler API. Uses `iter.Seq` for listing operations (Go 1.23 range-over-func). Transport is a proper interface. The `ServerSession.Elicit()` call-site pattern matches Go's context-passing conventions cleanly.

---

## yoloAI Current Usage

All MCP SDK usage is confined to `internal/mcpsrv/`:

| File | Lines | SDK surface used |
|---|---|---|
| `internal/mcpsrv/server.go` | 53 | `server.NewMCPServer`, `server.WithToolCapabilities`, `server.WithInstructions`, `server.ServeStdio` |
| `internal/mcpsrv/tools.go` | 660 | `mcp.NewTool`, `mcp.WithDescription`, `mcp.WithString`, `mcp.WithBoolean`, `mcp.WithNumber`, `mcp.Required`, `mcp.Description`, `mcp.CallToolRequest`, `mcp.CallToolResult`, `mcp.NewToolResultText` |
| `internal/mcpsrv/proxy.go` | 472 | No SDK imports — raw JSON-RPC stdio forwarding |
| `internal/cli/system_mcp.go` | 177 | No SDK imports — CLI flag wiring |

Import sites: `server.go:13` (`github.com/mark3labs/mcp-go/server`) and `tools.go:19` (`github.com/mark3labs/mcp-go/mcp`).

The `proxy.go` proxies stdio directly without using the SDK at all — it would not be affected by an SDK switch.

---

## Migration Cost Assessment

A migration from `mcp-go` to `go-sdk` would touch `server.go` and `tools.go` only.

**server.go (~53 lines):**
- Replace `server.NewMCPServer(...)` with `mcp.NewServer(&mcp.Implementation{...}, opts)`
- Replace `server.ServeStdio(s.srv)` with `s.srv.Run(ctx, &mcp.StdioTransport{})`
- Tool registration call site stays, but the `AddTool` signature changes

**tools.go (~660 lines, ~79 `mcp.` call sites):**
- All 11 tool definitions must be rewritten from functional-options style to struct-tag style (define one struct per tool, or use inline schemas)
- All handler signatures change from `(ctx, mcp.CallToolRequest) (*mcp.CallToolResult, error)` to `(ctx, *mcp.CallToolRequest, ToolInput) (*mcp.CallToolResult, any, error)`
- All `req.GetString()/GetBool()/GetNumber()` calls replaced by struct field access
- `mcp.NewToolResultText(text)` replaced by `&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}`

**Estimated effort:** 1–2 days of mechanical rewrite. No logic changes. No changes to `proxy.go`, `system_mcp.go`, or any non-MCP code. Tests must be updated to match new arg patterns.

**Risk of migration:** Low logic risk (purely API translation), moderate test surface. The official SDK's typed handlers would make the post-migration code harder to break accidentally.

---

## Risks and Caveats

### Staying with mcp-go
- **Not v1:** The v0.x label means breaking changes are possible at any release. In practice the project has been stable (7 open issues, low churn in public API), but there is no contractual stability guarantee.
- **No elicitation:** If yoloai's MCP server needs to prompt the outer agent for input mid-tool-call (e.g., "confirm this destructive operation"), it would need to implement elicitation manually or use the file-based Q&A pattern it already has. For the current two-layer workflow this is acceptable.
- **Community-maintained:** If mark3labs loses interest, the SDK could stagnate. The official SDK is a natural replacement.

### Migrating to go-sdk now
- **Goroutine panic risk:** Issue #958 (open) documents that 9 internal goroutines lack `recover()`. An unexpected panic in any of them crashes the host process (i.e., crashes `yoloai mcp serve`). This is a production reliability concern. It should be fixed before committing to the SDK.
- **More verbose result construction:** `mcp.NewToolResultText("text")` → `&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "text"}}}` is significantly more verbose. A wrapper would be needed.
- **API cost for tool definitions:** The struct-tag pattern requires more upfront boilerplate per tool (define a struct, add jsonschema tags) but pays off in handler body quality.

### Watch triggers for migration
- Official SDK closes #958 (panic recovery)
- yoloai needs elicitation
- mcp-go introduces a breaking API change
- go-sdk reaches a release that adds a `NewToolResultText` convenience helper (reducing verbosity)

---

## Source Links

- `github.com/modelcontextprotocol/go-sdk` — https://github.com/modelcontextprotocol/go-sdk
- Official SDK releases — https://github.com/modelcontextprotocol/go-sdk/releases
- Official SDK pkg.go.dev — https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp
- Open issue #958 (panic) — https://github.com/modelcontextprotocol/go-sdk/issues/958
- Open issue #961 (session overwrite) — https://github.com/modelcontextprotocol/go-sdk/issues/961
- `github.com/mark3labs/mcp-go` — https://github.com/mark3labs/mcp-go
- mcp-go pkg.go.dev — https://pkg.go.dev/github.com/mark3labs/mcp-go
- mcp-go elicitation search (zero results) — https://github.com/mark3labs/mcp-go/search?q=elicitation
- mcp-go issue #817 (elicitation error misreported) — https://github.com/mark3labs/mcp-go/issues/817
- yoloai MCP server — `/home/karl/yoloai/internal/mcpsrv/server.go`
- yoloai MCP tools — `/home/karl/yoloai/internal/mcpsrv/tools.go`
- yoloai go.mod — `/home/karl/yoloai/go.mod`
