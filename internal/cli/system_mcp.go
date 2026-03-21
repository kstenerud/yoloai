package cli

// ABOUTME: `yoloai mcp serve` — starts the yoloAI orchestration MCP server on stdio.
// ABOUTME: `yoloai mcp proxy` — proxies an inner MCP server through a sandbox.

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/mcpsrv"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mcp",
		Short:   "MCP server commands (orchestration and proxy)",
		GroupID: groupLifecycle,
	}
	cmd.AddCommand(newMCPServeCmd(), newMCPProxyCmd())
	return cmd
}

func newMCPServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
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
        "args": ["mcp", "serve"]
      }
    }
  }`,
		Args: cobra.NoArgs,
		RunE: runMCPServe,
	}
}

func runMCPServe(cmd *cobra.Command, _ []string) error {
	backend, warn := detectContainerBackend(resolveContainerBackendConfig())
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
		srv := mcpsrv.New(mgr)
		return srv.ServeStdio(ctx)
	})
}

func newMCPProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy [flags] <name> [workdir] -- <command> [args...]",
		Short: "Run an MCP server inside a sandbox and proxy its stdio",
		Long: `Run an MCP server inside a sandbox and proxy its stdio to the caller.

The sandbox is created automatically if it does not exist (requires workdir).
If the sandbox already exists, it is reused regardless of the workdir argument.
If the container is stopped, it is restarted.

The default agent is "idle" (sleep infinity) — the container runs but no AI
agent is started. Use --agent to run an AI agent alongside the inner MCP server.

Injects sandbox_diff into the tool surface. All other tools from the inner
server are forwarded transparently. The sandbox persists after the proxy exits
so you can inspect and apply changes with 'yoloai diff' and 'yoloai apply'.

Path placeholders in the inner command are expanded from sandbox metadata:

  {workdir}   Primary working directory (container-side path)
  {files}     File exchange directory (/yoloai/files/)
  {cache}     Cache directory (/yoloai/cache/)
  {dir:N}     Nth auxiliary directory mount path (0-indexed)

Examples:
  # New sandbox — workdir required
  yoloai mcp proxy mybox /path/to/project -- npx -y @modelcontextprotocol/server-filesystem {workdir}

  # Reuse existing sandbox
  yoloai mcp proxy mybox -- npx -y @modelcontextprotocol/server-filesystem {workdir}`,
		Args: cobra.ArbitraryArgs,
		RunE: runMCPProxy,
	}

	cmd.Flags().String("agent", "idle", "Agent to run in the container (default: idle)")
	cmd.Flags().String("model", "", "Model override")
	cmd.Flags().String("profile", "", "Profile name")
	cmd.Flags().StringSlice("dir", nil, "Auxiliary directory (same syntax as 'yoloai new -d')")
	cmd.Flags().Bool("replace", false, "Destroy and recreate the sandbox if it exists")
	cmd.Flags().String("backend", "", "Runtime backend")

	return cmd
}

func runMCPProxy(cmd *cobra.Command, args []string) error {
	// Split positional args at "--"
	dashIdx := cmd.ArgsLenAtDash()
	var positional, innerCmd []string
	if dashIdx < 0 {
		positional = args
	} else {
		positional = args[:dashIdx]
		innerCmd = args[dashIdx:]
	}

	if len(positional) < 1 {
		return fmt.Errorf("sandbox name is required")
	}
	if len(innerCmd) == 0 {
		return fmt.Errorf("inner command required after '--'")
	}

	name := positional[0]
	rawWorkdir := ""
	if len(positional) >= 2 {
		rawWorkdir = positional[1]
	}

	agentFlag, _ := cmd.Flags().GetString("agent")
	model := resolveModel(cmd)
	profile := resolveProfile(cmd)
	rawDirs, _ := cmd.Flags().GetStringSlice("dir")
	replace, _ := cmd.Flags().GetBool("replace")

	// Parse workdir if provided
	var workdirSpec sandbox.DirSpec
	if rawWorkdir != "" {
		parsed, err := sandbox.ParseDirArg(rawWorkdir)
		if err != nil {
			return fmt.Errorf("invalid workdir: %w", err)
		}
		workdirSpec = sandbox.DirArgToSpec(parsed)
		if workdirSpec.Mode == "" {
			workdirSpec.Mode = sandbox.DirModeCopy
		}
	}

	// Parse aux dirs
	var auxDirSpecs []sandbox.DirSpec
	for _, rawDir := range rawDirs {
		parsed, err := sandbox.ParseDirArg(rawDir)
		if err != nil {
			return fmt.Errorf("invalid directory %q: %w", rawDir, err)
		}
		auxDirSpecs = append(auxDirSpecs, sandbox.DirArgToSpec(parsed))
	}

	opts := mcpsrv.ProxyOptions{
		Workdir: workdirSpec,
		AuxDirs: auxDirSpecs,
		Agent:   agentFlag,
		Model:   model,
		Profile: profile,
		Replace: replace,
	}

	backend := resolveBackendForSandbox(name)
	return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
		proxy := mcpsrv.NewProxy(mgr, name, innerCmd, opts)
		return proxy.ServeStdio(ctx)
	})
}
