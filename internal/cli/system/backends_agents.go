package system

// ABOUTME: Backend and agent info commands, plus the shared probe-based
// ABOUTME: checkBackend helper. All static facts come from runtime.Descriptors();
// ABOUTME: only the per-backend tradeoffs (CLI-presentation language) live here.

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// backendTradeoffs is the bullet-list shown under `yoloai system backends
// <name>`. Pros/cons are CLI-presentation language (a selling pitch) rather
// than runtime metadata; per the round-7 critique resolution they stay here
// instead of going on BackendDescriptor. Backends without an entry render
// the table without a Tradeoffs section — which is the right default for
// any future backend that hasn't been editorially described yet.
var backendTradeoffs = map[string][]string{
	"docker": {
		"Most portable — works everywhere Docker runs",
		"Containers are lightweight and fast to create",
		"Always provides a Linux environment, even on macOS/Windows hosts",
		"File I/O on macOS may be slower due to VirtioFS layer",
	},
	"podman": {
		"Daemonless — no background service required",
		"Rootless by default — better security posture",
		"Docker-compatible — uses same container images and commands",
		"Podman socket must be started manually (systemctl --user start podman.socket)",
	},
	"tart": {
		"Native macOS environment for macOS-specific development (Xcode, Swift, etc.)",
		"Stronger isolation than seatbelt — full VM boundary",
		"Heavier than containers — VMs take longer to create and use more resources",
		"Apple Silicon only — does not work on Intel Macs",
	},
	"seatbelt": {
		"Lightest weight — no container or VM overhead, near-instant startup",
		"Runs natively on the host with the same tools already installed",
		"Less isolation than Docker or Tart — process-level sandbox only",
		"sandbox-exec is deprecated by Apple but still functional",
	},
	"containerd": {
		"Strongest isolation — each sandbox runs in a separate hardware VM (Kata + QEMU or Firecracker)",
		"Host kernel is not directly reachable from inside the sandbox",
		"~1-2s additional startup overhead per sandbox vs Docker containers",
		"~100-150 MB per-sandbox VM memory overhead",
		"Requires KVM — not available on standard cloud VMs without nested virtualization",
		"Used automatically with --isolation vm or --isolation vm-enhanced",
	},
}

func newSystemBackendsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backends [name]",
		Short: "List available runtime backends",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			descs := runtime.Descriptors()
			names := make([]string, len(descs))
			for i, d := range descs {
				names[i] = d.Name
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return showBackendDetail(cmd, args[0])
			}
			return listBackends(cmd)
		},
	}
}

// listBackends displays the summary table of all backends, iterating
// runtime.Descriptors() rather than a CLI-local list — new backends
// register themselves and auto-appear in this listing.
func listBackends(cmd *cobra.Command) error {
	ctx := cmd.Context()
	descs := runtime.Descriptors()

	if cliutil.JSONEnabled(cmd) {
		type backendJSON struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Available   bool   `json:"available"`
			Note        string `json:"note,omitempty"`
		}
		items := make([]backendJSON, 0, len(descs))
		for _, d := range descs {
			available, note := cliutil.CheckBackend(ctx, d.Name)
			items = append(items, backendJSON{
				Name:        d.Name,
				Description: d.Description,
				Available:   available,
				Note:        note,
			})
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), items)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "BACKEND\tDESCRIPTION\tAVAILABLE\tNOTE") //nolint:errcheck // best-effort output

	for _, d := range descs {
		available, note := cliutil.CheckBackend(ctx, d.Name)
		avail := "yes"
		if !available {
			avail = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.Name, d.Description, avail, note) //nolint:errcheck // best-effort output
	}

	return w.Flush()
}

// showBackendDetail displays detailed information about a single backend.
// Descriptor fields supply the operational metadata; backendTradeoffs is
// the CLI-only selling-pitch bullet list (kept separate per round-7 critique).
func showBackendDetail(cmd *cobra.Command, name string) error {
	desc, ok := runtime.Descriptor(name)
	if !ok {
		return sandbox.NewUsageError("unknown backend %q (valid: %s)", name, strings.Join(backendNames(), ", "))
	}
	tradeoffs := backendTradeoffs[name]

	ctx := cmd.Context()

	if cliutil.JSONEnabled(cmd) {
		available, note := cliutil.CheckBackend(ctx, desc.Name)
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
			"name":         desc.Name,
			"description":  desc.Description,
			"available":    available,
			"note":         note,
			"platforms":    desc.Platforms,
			"requires":     desc.Requires,
			"install_hint": desc.InstallHint,
			"tradeoffs":    tradeoffs,
		})
	}

	out := cmd.OutOrStdout()

	available, note := cliutil.CheckBackend(ctx, name)
	avail := "yes"
	if !available {
		avail = "no"
		if note != "" {
			avail += " (" + note + ")"
		}
	}

	fmt.Fprintf(out, "Backend:     %s\n", desc.Name)                          //nolint:errcheck
	fmt.Fprintf(out, "Description: %s\n", desc.Description)                   //nolint:errcheck
	fmt.Fprintf(out, "Available:   %s\n", avail)                              //nolint:errcheck
	fmt.Fprintf(out, "Platforms:   %s\n", strings.Join(desc.Platforms, ", ")) //nolint:errcheck
	fmt.Fprintf(out, "Requires:    %s\n", desc.Requires)                      //nolint:errcheck
	if desc.InstallHint != "" {
		fmt.Fprintf(out, "Install:     %s\n", desc.InstallHint) //nolint:errcheck
	}
	if len(tradeoffs) > 0 {
		fmt.Fprintln(out)               //nolint:errcheck
		fmt.Fprintln(out, "Tradeoffs:") //nolint:errcheck
		for _, t := range tradeoffs {
			fmt.Fprintf(out, "  - %s\n", t) //nolint:errcheck
		}
	}

	return nil
}

// backendNames returns sorted names of all registered backends; used in
// usage-error messages enumerating valid choices.
func backendNames() []string {
	descs := runtime.Descriptors()
	names := make([]string, len(descs))
	for i, d := range descs {
		names[i] = d.Name
	}
	return names
}

func newSystemAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents [name]",
		Short: "List available agents",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return agent.AllAgentNames(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return showAgentDetail(cmd, args[0])
			}
			return listAgents(cmd)
		},
	}
}

// listAgents displays the summary table of all agents.
func listAgents(cmd *cobra.Command) error {
	if cliutil.JSONEnabled(cmd) {
		type agentJSON struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			PromptMode  string `json:"prompt_mode"`
		}
		var items []agentJSON
		for _, name := range agent.AllAgentNames() {
			def := agent.GetAgent(name)
			items = append(items, agentJSON{
				Name:        def.Name,
				Description: def.Description,
				PromptMode:  string(def.PromptMode),
			})
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), items)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "AGENT\tDESCRIPTION\tPROMPT MODE") //nolint:errcheck

	for _, name := range agent.AllAgentNames() {
		def := agent.GetAgent(name)
		fmt.Fprintf(w, "%s\t%s\t%s\n", def.Name, def.Description, def.PromptMode) //nolint:errcheck
	}

	return w.Flush()
}

// showAgentDetail displays detailed information about a single agent.
func showAgentDetail(cmd *cobra.Command, name string) error {
	def := agent.GetAgent(name)
	if def == nil {
		return sandbox.NewUsageError("unknown agent %q (valid: %s)", name, strings.Join(agent.AllAgentNames(), ", "))
	}

	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Agent:       %s\n", def.Name)        //nolint:errcheck
	fmt.Fprintf(out, "Description: %s\n", def.Description) //nolint:errcheck
	fmt.Fprintf(out, "Prompt mode: %s\n", def.PromptMode)  //nolint:errcheck

	if len(def.APIKeyEnvVars) > 0 {
		fmt.Fprintf(out, "API keys:    %s\n", strings.Join(def.APIKeyEnvVars, ", ")) //nolint:errcheck
	}
	if def.StateDir != "" {
		fmt.Fprintf(out, "State dir:   %s\n", def.StateDir) //nolint:errcheck
	}
	if def.ModelFlag != "" {
		fmt.Fprintf(out, "Model flag:  %s\n", def.ModelFlag) //nolint:errcheck
	}

	if len(def.ModelAliases) > 0 {
		fmt.Fprintln(out)                   //nolint:errcheck
		fmt.Fprintln(out, "Model aliases:") //nolint:errcheck
		for alias, model := range def.ModelAliases {
			fmt.Fprintf(out, "  %s → %s\n", alias, model) //nolint:errcheck
		}
	}

	return nil
}
