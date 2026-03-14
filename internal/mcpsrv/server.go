// Package mcpsrv implements the yoloAI MCP server, exposing sandbox
// operations as tools for outer agents driving the two-layer agentic workflow.
package mcpsrv

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/mark3labs/mcp-go/server"
)

// Server is the yoloAI orchestration MCP server.
// It exposes sandbox lifecycle, observation, refinement, and file exchange
// tools for outer agents (Claude Desktop, VS Code Copilot, etc.).
type Server struct {
	mgr *sandbox.Manager
	srv *server.MCPServer
}

// serverInstructions is returned in the initialize response and gives the outer
// agent a high-level orientation before it calls any tools.
const serverInstructions = `yoloAI runs AI coding agents (Claude Code, Gemini, Codex) inside isolated
Docker containers. The outer agent (you) drives the inner agent via these tools,
then inspects and applies the results.

Call yoloai_help for the full workflow guide.`

// New creates a new orchestration MCP server backed by mgr.
func New(mgr *sandbox.Manager) *Server {
	s := &Server{mgr: mgr}
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
func (s *Server) ServeStdio(ctx context.Context) error {
	return server.ServeStdio(s.srv)
}

// errorf formats an [ERROR] prefixed string for MCP tool responses.
// Outer agents can parse this prefix to distinguish errors from normal output.
func errorf(format string, args ...any) string {
	return "[ERROR] " + fmt.Sprintf(format, args...)
}
