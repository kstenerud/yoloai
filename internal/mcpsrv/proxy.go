package mcpsrv

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/kstenerud/yoloai/sandbox"
)

// ProxyServer proxies an MCP server running inside a sandbox.
// It forwards stdio between the outer agent and the inner MCP process,
// injecting sandbox_diff into the tool surface.
type ProxyServer struct {
	mgr         *sandbox.Manager
	sandboxName string
	innerCmd    []string
}

// NewProxy creates a new proxy server.
func NewProxy(mgr *sandbox.Manager, sandboxName string, innerCmd []string) *ProxyServer {
	return &ProxyServer{
		mgr:         mgr,
		sandboxName: sandboxName,
		innerCmd:    innerCmd,
	}
}

// ServeStdio runs the proxy on stdin/stdout.
func (p *ProxyServer) ServeStdio(ctx context.Context) error {
	return p.run(ctx, os.Stdin, os.Stdout)
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

// expandCmd substitutes path placeholders in the inner command args using
// sandbox metadata, so callers don't need to hardcode container-side paths.
//
// Supported placeholders:
//
//	{workdir}  — meta.Workdir.MountPath (the primary working directory)
//	{files}    — the file exchange directory (/yoloai/files/)
//	{cache}    — the cache directory (/yoloai/cache/)
//	{dir:N}    — meta.Directories[N].MountPath (Nth auxiliary directory, 0-indexed)
func expandCmd(cmd []string, meta *sandbox.Meta) ([]string, error) {
	filesDir := "/yoloai/files/"
	cacheDir := "/yoloai/cache/"
	if meta.Backend == "seatbelt" {
		filesDir = sandbox.FilesDir(meta.Name)
		cacheDir = sandbox.CacheDir(meta.Name)
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

func (p *ProxyServer) run(ctx context.Context, in io.Reader, out io.Writer) error {
	// Load sandbox metadata for path template expansion.
	meta, err := sandbox.LoadMeta(sandbox.Dir(p.sandboxName))
	if err != nil {
		return fmt.Errorf("load sandbox %q metadata: %w", p.sandboxName, err)
	}

	innerCmd, err := expandCmd(p.innerCmd, meta)
	if err != nil {
		return fmt.Errorf("expand inner command: %w", err)
	}

	// Start inner MCP server via docker exec
	containerName := sandbox.InstanceName(p.sandboxName)
	dockerArgs := append([]string{"exec", "-i", containerName}, innerCmd...)
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...) //nolint:gosec // G204: innerCmd is user-provided

	innerIn, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("get inner stdin pipe: %w", err)
	}
	innerOut, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("get inner stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start inner MCP server in sandbox %q: %w", p.sandboxName, err)
	}
	defer cmd.Wait() //nolint:errcheck // best-effort

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

	// Forward inner→outer in a goroutine, intercepting tools/list results
	innerDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(innerOut)
		scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			var msg jsonRPCMsg
			if err := json.Unmarshal(line, &msg); err != nil {
				// Not valid JSON-RPC — forward as-is
				outMu.Lock()
				fmt.Fprintf(out, "%s\n", line) //nolint:errcheck
				outMu.Unlock()
				continue
			}

			// If this response is for a locally-handled ID, skip it
			if msg.ID != nil {
				idStr := string(msg.ID)
				localMu.Lock()
				isLocal := localIDs[idStr]
				if isLocal {
					delete(localIDs, idStr)
				}
				localMu.Unlock()
				if isLocal {
					continue // discard inner response for local IDs
				}
			}

			// Intercept tools/list result to inject our tools
			if msg.Result != nil && msg.ID != nil {
				var result map[string]json.RawMessage
				if err := json.Unmarshal(msg.Result, &result); err == nil {
					if _, hasTools := result["tools"]; hasTools {
						var tools []json.RawMessage
						if err := json.Unmarshal(result["tools"], &tools); err == nil {
							// Append injected tools
							for _, t := range injectedToolDefs {
								toolJSON, marshalErr := json.Marshal(t)
								if marshalErr == nil {
									tools = append(tools, toolJSON)
								}
							}
							result["tools"], _ = json.Marshal(tools)
							msg.Result, _ = json.Marshal(result)
						}
					}
				}
			}

			if err := writeOut(msg); err != nil {
				innerDone <- err
				return
			}
		}
		innerDone <- scanner.Err()
	}()

	// Read from outer agent and forward/intercept
	outerScanner := bufio.NewScanner(in)
	outerScanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for outerScanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := outerScanner.Bytes()
		var msg jsonRPCMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			// Not valid JSON-RPC — forward as-is
			fmt.Fprintln(innerIn, string(line)) //nolint:errcheck
			continue
		}

		// Check if this is a tool/call for one of our injected tools
		if msg.Method == "tools/call" {
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(msg.Params, &params); err == nil && params.Name == "sandbox_diff" {
				result := p.handleProxyDiff(params.Arguments)
				resp := jsonRPCMsg{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result:  mustMarshal(result),
				}
				if err := writeOut(resp); err != nil {
					return err
				}
				// Mark this ID as locally handled so we discard any inner response
				if msg.ID != nil {
					localMu.Lock()
					localIDs[string(msg.ID)] = true
					localMu.Unlock()
				}
				// Don't forward to inner — we handled it locally.
				continue
			}
		}

		// Forward to inner
		if _, err := fmt.Fprintln(innerIn, string(line)); err != nil {
			return fmt.Errorf("write to inner MCP server: %w", err)
		}
	}

	if err := outerScanner.Err(); err != nil {
		return err
	}

	// Close inner stdin to signal EOF
	_ = innerIn.Close()

	return <-innerDone
}

// handleProxyDiff handles sandbox_diff tool calls for the proxy server.
func (p *ProxyServer) handleProxyDiff(args map[string]any) map[string]any {
	stat, _ := args["stat"].(bool)

	results, err := sandbox.GenerateMultiDiff(p.sandboxName, stat)
	if err != nil {
		return mcpTextContent(errorf("diff sandbox %q: %v", p.sandboxName, err))
	}

	if len(results) == 0 {
		return mcpTextContent("[ERROR] no changes to diff")
	}

	var parts []string
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("--- %s ---\n%s", r.WorkDir, r.Output))
	}

	return mcpTextContent(strings.Join(parts, "\n\n"))
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
