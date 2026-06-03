// ABOUTME: ProxyServer wraps any MCP server running inside a sandbox, forwarding
// ABOUTME: JSON-RPC stdio while injecting the sandbox_diff tool transparently.
package mcpsrv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/kstenerud/yoloai"
)

// ProxyOptions controls how the proxy server creates or reuses a sandbox.
type ProxyOptions struct {
	// Workdir is the primary working directory. Required when creating a new
	// sandbox; ignored if the sandbox already exists.
	Workdir yoloai.DirSpec

	// AuxDirs are auxiliary directories to mount. Used only when creating.
	AuxDirs []yoloai.DirSpec

	// Agent is the agent to run in the container. Defaults to "idle"
	// (sleep infinity — keeps the container alive without an AI agent).
	Agent string

	// Model, Profile are passed to Engine.Create when creating.
	Model   string
	Profile string

	// Replace destroys any existing sandbox before creating a new one.
	Replace bool
}

// ProxyServer proxies an MCP server running inside a sandbox.
// It owns the sandbox lifecycle: creating or reusing the sandbox,
// ensuring the container is running, and forwarding stdio between the
// outer agent and the inner MCP process.
type ProxyServer struct {
	c           *yoloai.Client
	sandboxName string
	innerCmd    []string
	opts        ProxyOptions
}

// NewProxy creates a new proxy server. The Client is the caller's;
// ServeStdio does not close it.
func NewProxy(c *yoloai.Client, sandboxName string, innerCmd []string, opts ProxyOptions) *ProxyServer {
	if opts.Agent == "" {
		opts.Agent = "idle"
	}
	return &ProxyServer{
		c:           c,
		sandboxName: sandboxName,
		innerCmd:    innerCmd,
		opts:        opts,
	}
}

// ServeStdio ensures the sandbox is running, then proxies stdin/stdout to
// the inner MCP server for the duration of the connection.
func (p *ProxyServer) ServeStdio(ctx context.Context) error {
	if err := p.c.EnsureSetup(ctx); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	meta, err := p.ensureRunning(ctx)
	if err != nil {
		return err
	}

	sb, err := p.c.Sandbox(p.sandboxName)
	if err != nil {
		return fmt.Errorf("sandbox handle %q: %w", p.sandboxName, err)
	}
	innerCmd, err := expandCmd(p.innerCmd, sb.Files().Path(), sb.CacheDir(), meta)
	if err != nil {
		return fmt.Errorf("expand inner command: %w", err)
	}

	return p.run(ctx, os.Stdin, os.Stdout, meta, innerCmd)
}

// ensureRunning guarantees the sandbox container is running, creating it if
// needed. Returns the sandbox metadata for path template expansion.
func (p *ProxyServer) ensureRunning(ctx context.Context) (*yoloai.Environment, error) {
	sb, sbErr := p.c.Sandbox(p.sandboxName)
	if errors.Is(sbErr, yoloai.ErrSandboxNotFound) {
		return p.createSandbox(ctx)
	}
	if sbErr != nil {
		return nil, fmt.Errorf("sandbox handle %q: %w", p.sandboxName, sbErr)
	}

	info, err := sb.Inspect(ctx)

	if errors.Is(err, yoloai.ErrSandboxNotFound) {
		return p.createSandbox(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect sandbox %q: %w", p.sandboxName, err)
	}

	// Sandbox exists — check container state
	switch info.Status {
	case yoloai.StatusActive, yoloai.StatusIdle, yoloai.StatusDone, yoloai.StatusFailed:
		// Container is running — use as-is
		return info.Environment, nil

	case yoloai.StatusStopped, yoloai.StatusRemoved:
		// Container stopped or removed — restart it (auto-start; notices discarded)
		if _, err := sb.Start(ctx, yoloai.StartOptions{}); err != nil {
			return nil, fmt.Errorf("start sandbox %q: %w", p.sandboxName, err)
		}
		return info.Environment, nil

	default:
		return nil, fmt.Errorf("sandbox %q is in unexpected state %q", p.sandboxName, info.Status)
	}
}

// createSandbox creates a new sandbox with the proxy's options.
func (p *ProxyServer) createSandbox(ctx context.Context) (*yoloai.Environment, error) {
	if p.opts.Workdir.Path == "" {
		return nil, fmt.Errorf("sandbox %q does not exist — provide --workdir to create it", p.sandboxName)
	}

	opts := yoloai.SandboxCreateOptions{
		Name:      p.sandboxName,
		Workdir:   p.opts.Workdir,
		AuxDirs:   p.opts.AuxDirs,
		AgentType: yoloai.AgentType(p.opts.Agent),
		Model:     p.opts.Model,
		Profile:   p.opts.Profile,
		Replace:   p.opts.Replace,
		// Proxy auto-create is non-interactive: proceed on a dirty workdir.
		AllowDirtyWorkdir: true,
	}

	if _, err := p.c.Create(ctx, opts); err != nil {
		return nil, fmt.Errorf("create sandbox %q: %w", p.sandboxName, err)
	}

	sb, err := p.c.Sandbox(p.sandboxName)
	if err != nil {
		return nil, fmt.Errorf("sandbox handle %q after create: %w", p.sandboxName, err)
	}
	info, err := sb.Inspect(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect sandbox %q after create: %w", p.sandboxName, err)
	}
	return info.Environment, nil
}

// expandCmd substitutes path placeholders in the inner command args using
// sandbox metadata, so callers don't need to hardcode container-side paths.
//
// Supported placeholders:
//
//	{workdir}  — meta.Workdir.MountPath (the primary working directory)
//	{files}    — the file exchange directory (/yoloai/files/)
//	{cache}    — the cache directory (/yoloai/cache/)
//	{dir:N}    — meta.Directories[N].MountPath (Nth auxiliary directory, 0-indexed)
//
// hostFilesDir/hostCacheDir are the on-host file-exchange and cache
// directories (from Sandbox.Files().Path()/Sandbox.CacheDir()). They are used only when
// meta.HostFilesystem is true; for container backends the fixed in-container
// paths are used instead and these arguments are ignored.
func expandCmd(cmd []string, hostFilesDir, hostCacheDir string, meta *yoloai.Environment) ([]string, error) {
	filesDir := "/yoloai/files/"
	cacheDir := "/yoloai/cache/"
	if meta.HostFilesystem {
		filesDir = hostFilesDir
		cacheDir = hostCacheDir
	}

	expanded := make([]string, len(cmd))
	for i, arg := range cmd {
		arg = strings.ReplaceAll(arg, "{workdir}", meta.Workdir.MountPath)
		arg = strings.ReplaceAll(arg, "{files}", filesDir)
		arg = strings.ReplaceAll(arg, "{cache}", cacheDir)

		// {dir:N} — auxiliary directory by index
		for {
			start := strings.Index(arg, "{dir:")
			if start == -1 {
				break
			}
			end := strings.Index(arg[start:], "}")
			if end == -1 {
				break
			}
			end += start
			indexStr := arg[start+5 : end]
			n, err := strconv.Atoi(indexStr)
			if err != nil || n < 0 || n >= len(meta.Directories) {
				return nil, fmt.Errorf("placeholder {dir:%s}: index out of range (sandbox has %d auxiliary directories)", indexStr, len(meta.Directories))
			}
			arg = arg[:start] + meta.Directories[n].MountPath + arg[end+1:]
		}

		expanded[i] = arg
	}
	return expanded, nil
}

// jsonRPCMsg is a minimal JSON-RPC 2.0 message envelope.
type jsonRPCMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// injectedToolDefs are tool definitions injected into tools/list responses.
var injectedToolDefs = []map[string]any{
	{
		"name":        "sandbox_diff",
		"description": "Show a diff of all changes made in the sandbox. Call with stat=true for a cheap summary first.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"stat": map[string]any{
					"type":        "boolean",
					"description": "Return stat summary only (default false)",
				},
			},
		},
	},
}

func (p *ProxyServer) run(ctx context.Context, in io.Reader, out io.Writer, _ *yoloai.Environment, innerCmd []string) error {
	// Run the inner MCP server inside the sandbox via Client.StdioExec.
	// Backends that don't implement runtime.StdioExecer (Tart, Seatbelt)
	// surface as a *UsageError from StdioExec.
	innerInRead, innerIn := io.Pipe()
	innerOut, innerOutWrite := io.Pipe()

	// Run the inner exec in a goroutine; on exit, close the pipes so the
	// outer reader and writer loops below see EOF and unwind.
	execDone := make(chan error, 1)
	go func() {
		sb, err := p.c.Sandbox(p.sandboxName)
		if err == nil {
			err = sb.Exec(ctx, yoloai.ExecOptions{Command: innerCmd}, yoloai.IOStreams{In: innerInRead, Out: innerOutWrite, Err: os.Stderr})
		}
		_ = innerOutWrite.Close()
		_ = innerInRead.Close()
		execDone <- err
	}()
	defer func() {
		_ = innerIn.Close()
		_ = innerOut.Close()
		<-execDone // wait for goroutine to finish before returning
	}()

	// localIDs tracks IDs of tool/call requests we handle locally.
	localIDs := make(map[string]bool)
	var localMu sync.Mutex

	outMu := &sync.Mutex{}
	writeOut := func(msg jsonRPCMsg) error {
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		outMu.Lock()
		defer outMu.Unlock()
		_, err = fmt.Fprintf(out, "%s\n", data)
		return err
	}

	// Forward inner→outer, intercepting tools/list results to inject our tools
	innerDone := make(chan error, 1)
	go p.forwardInnerToOuter(innerOut, out, outMu, &localMu, localIDs, writeOut, innerDone)

	// Read from outer agent, handle injected tools locally, forward the rest
	outerScanner := bufio.NewScanner(in)
	outerScanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for outerScanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := p.handleOuterMessage(outerScanner.Bytes(), innerIn, &localMu, localIDs, writeOut); err != nil {
			return err
		}
	}

	if err := outerScanner.Err(); err != nil {
		return err
	}

	_ = innerIn.Close()
	return <-innerDone
}

// forwardInnerToOuter reads from innerOut and forwards messages to out,
// discarding locally-handled responses and injecting tools into tools/list results.
func (p *ProxyServer) forwardInnerToOuter(
	innerOut io.Reader,
	out io.Writer,
	outMu *sync.Mutex,
	localMu *sync.Mutex,
	localIDs map[string]bool,
	writeOut func(jsonRPCMsg) error,
	done chan<- error,
) {
	scanner := bufio.NewScanner(innerOut)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg jsonRPCMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			outMu.Lock()
			fmt.Fprintf(out, "%s\n", line) //nolint:errcheck,gosec // G705: intentional proxy forwarding
			outMu.Unlock()
			continue
		}

		// Discard responses for locally-handled requests
		if discardLocalResponse(&msg, localMu, localIDs) {
			continue
		}

		injectToolsIfNeeded(&msg)

		if err := writeOut(msg); err != nil {
			done <- err
			return
		}
	}
	done <- scanner.Err()
}

// discardLocalResponse checks if msg is a response for a locally-handled request
// and removes it from the tracking map. Returns true if the message should be discarded.
func discardLocalResponse(msg *jsonRPCMsg, localMu *sync.Mutex, localIDs map[string]bool) bool {
	if msg.ID == nil {
		return false
	}
	idStr := string(msg.ID)
	localMu.Lock()
	isLocal := localIDs[idStr]
	if isLocal {
		delete(localIDs, idStr)
	}
	localMu.Unlock()
	return isLocal
}

// injectToolsIfNeeded modifies a tools/list response to include injected tool definitions.
func injectToolsIfNeeded(msg *jsonRPCMsg) {
	if msg.Result == nil || msg.ID == nil {
		return
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return
	}
	if _, hasTools := result["tools"]; !hasTools {
		return
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(result["tools"], &tools); err != nil {
		return
	}
	for _, t := range injectedToolDefs {
		if toolJSON, marshalErr := json.Marshal(t); marshalErr == nil {
			tools = append(tools, toolJSON)
		}
	}
	result["tools"], _ = json.Marshal(tools)
	msg.Result, _ = json.Marshal(result)
}

// handleOuterMessage processes a single message from the outer agent.
// It handles injected tool calls locally and forwards everything else to innerIn.
func (p *ProxyServer) handleOuterMessage(
	line []byte,
	innerIn io.Writer,
	localMu *sync.Mutex,
	localIDs map[string]bool,
	writeOut func(jsonRPCMsg) error,
) error {
	var msg jsonRPCMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		fmt.Fprintln(innerIn, string(line)) //nolint:errcheck,gosec // G705: intentional proxy forwarding
		return nil                          //nolint:nilerr // intentional: forward raw line when JSON is unparseable
	}

	if msg.Method == "tools/call" {
		if handled, err := p.tryHandleLocalToolCall(msg, localMu, localIDs, writeOut); handled {
			return err
		}
	}

	if _, err := fmt.Fprintln(innerIn, string(line)); err != nil { //nolint:gosec // G705: intentional proxy forwarding
		return fmt.Errorf("write to inner MCP server: %w", err)
	}
	return nil
}

// tryHandleLocalToolCall attempts to handle a tools/call message for locally-injected tools.
// Returns (true, err) if the message was handled locally, (false, nil) otherwise.
func (p *ProxyServer) tryHandleLocalToolCall(
	msg jsonRPCMsg,
	localMu *sync.Mutex,
	localIDs map[string]bool,
	writeOut func(jsonRPCMsg) error,
) (bool, error) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil || params.Name != "sandbox_diff" {
		return false, nil //nolint:nilerr // intentional: non-matching calls are forwarded to inner server
	}

	result := p.handleProxyDiff(params.Arguments)
	resp := jsonRPCMsg{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  mustMarshal(result),
	}
	if err := writeOut(resp); err != nil {
		return true, err
	}
	if msg.ID != nil {
		localMu.Lock()
		localIDs[string(msg.ID)] = true
		localMu.Unlock()
	}
	return true, nil
}

// handleProxyDiff handles sandbox_diff tool calls locally.
func (p *ProxyServer) handleProxyDiff(args map[string]any) map[string]any {
	stat, _ := args["stat"].(bool)

	sb, err := p.c.Sandbox(p.sandboxName)
	if err != nil {
		return mcpTextContent(errorf("sandbox handle %q: %v", p.sandboxName, err))
	}
	diff, err := sb.Workdir().Diff(context.Background(), yoloai.DiffOptions{Stat: stat})
	if err != nil {
		return mcpTextContent(errorf("diff sandbox %q: %v", p.sandboxName, err))
	}

	if diff == "" {
		return mcpTextContent("[ERROR] no changes to diff")
	}
	return mcpTextContent(diff)
}

// mcpTextContent returns an MCP tool result with a single text content item.
func mcpTextContent(text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}
}

// mustMarshal marshals v to JSON, returning null on error.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return data
}
