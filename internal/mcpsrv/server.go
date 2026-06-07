// ABOUTME: Server type and ServeStdio entry point for the orchestration MCP server.
// ABOUTME: Outer agents (Claude Desktop, etc.) call its tools to drive inner sandboxes.

// Package mcpsrv implements the yoloAI MCP server, exposing sandbox
// operations as tools for outer agents driving the two-layer agentic workflow.
package mcpsrv

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai"
	"github.com/mark3labs/mcp-go/server"
)

// Server is the yoloAI orchestration MCP server.
// It exposes sandbox lifecycle, observation, refinement, and file exchange
// tools for outer agents (Claude Desktop, VS Code Copilot, etc.).
type Server struct {
	client *yoloai.Client
	srv    *server.MCPServer
}

// serverInstructions is returned in the initialize response and gives the outer
// agent a high-level orientation before it calls any tools.
const serverInstructions = `yoloAI runs AI coding agents (Claude Code, Gemini, Codex) inside isolated
Docker containers. The outer agent (you) drives the inner agent via these tools,
then inspects and applies the results.

Call yoloai_help for the full workflow guide.`

// New creates a new orchestration MCP server backed by c. The Client is the
// caller's; ServeStdio does not close it.
func New(c *yoloai.Client) *Server {
	s := &Server{client: c}
	s.srv = server.NewMCPServer(
		"yoloai",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithInstructions(serverInstructions),
	)
	s.registerTools()
	return s
}

// ServeStdio runs the MCP server on stdin/stdout until ctx is cancelled.
func (s *Server) ServeStdio(_ context.Context) error {
	return server.ServeStdio(s.srv)
}

// errorf formats an [ERROR] prefixed string for MCP tool responses.
// Outer agents can parse this prefix to distinguish errors from normal output.
func errorf(format string, args ...any) string {
	return "[ERROR] " + fmt.Sprintf(format, args...)
}
