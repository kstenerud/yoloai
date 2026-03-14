package cli

// ABOUTME: `yoloai system mcp` — starts the yoloAI orchestration MCP server on stdio.
// ABOUTME: `yoloai system mcp-proxy` — proxies an inner MCP server through a sandbox.

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/internal/mcpsrv"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the yoloAI MCP server (stdio)",
		Long: `Start the yoloAI MCP server on stdin/stdout.

The MCP server exposes sandbox operations as tools for outer agents
(Claude Desktop, VS Code Copilot, etc.) driving a two-layer agentic workflow:

  - sandbox_create / sandbox_status / sandbox_list / sandbox_destroy
  - sandbox_diff / sandbox_diff_file / sandbox_log
  - sandbox_input / sandbox_reset
  - sandbox_files_list / sandbox_files_read / sandbox_files_write

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

func runMCP(cmd *cobra.Command, _ []string) error {
	backend := resolveBackendFromConfig()
	return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
		srv := mcpsrv.New(mgr)
		return srv.ServeStdio(ctx)
	})
}

func newMCPProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp-proxy <name> -- <command> [args...]",
		Short: "Proxy an MCP server running inside a sandbox (stdio)",
		Long: `Run an MCP server inside a sandbox and proxy its stdio to the caller.

Injects sandbox_diff into the tool surface. The proxied server's tools
are forwarded transparently.

The sandbox must already exist (use 'yoloai new' or the MCP sandbox_create tool).

Example:
  yoloai system mcp-proxy mybox -- npx -y @modelcontextprotocol/server-filesystem /workspace`,
		Args: cobra.MinimumNArgs(1),
		RunE: runMCPProxy,
	}

	return cmd
}

func runMCPProxy(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Find the "--" separator
	var innerCmd []string
	for i, a := range args {
		if a == "--" {
			innerCmd = args[i+1:]
			break
		}
	}
	if len(innerCmd) == 0 {
		return fmt.Errorf("inner command required: yoloai system mcp-proxy <name> -- <command> [args...]")
	}

	backend := resolveBackendForSandbox(name)
	return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
		proxy := mcpsrv.NewProxy(mgr, name, innerCmd)
		return proxy.ServeStdio(ctx)
	})
}
