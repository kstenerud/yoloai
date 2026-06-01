package system

// ABOUTME: Backend and agent info commands, plus the shared probe-based
// ABOUTME: checkBackend helper. All static facts come from runtime.Descriptors();
// ABOUTME: only the per-backend tradeoffs (CLI-presentation language) live here.

import (
	"fmt"
	"strings"
	"text/tabwriter"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/yoerrors"
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
		ValidArgsFunction: func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return backendNames(cmd), cobra.ShellCompDirectiveNoFileComp
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
	backends := cliutil.NewSystemClient().Backends(cmd.Context(), yoloai.BackendQuery{ProbeAvailability: true})

	if cliutil.JSONEnabled(cmd) {
		type backendJSON struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Available   bool   `json:"available"`
			Note        string `json:"note,omitempty"`
		}
		items := make([]backendJSON, 0, len(backends))
		for _, b := range backends {
			items = append(items, backendJSON{
				Name:        string(b.Name),
				Description: b.Description,
				Available:   b.Available,
				Note:        b.Note,
			})
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), items)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "BACKEND\tDESCRIPTION\tAVAILABLE\tNOTE") //nolint:errcheck // best-effort output

	for _, b := range backends {
		avail := "yes"
		if !b.Available {
			avail = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Name, b.Description, avail, b.Note) //nolint:errcheck // best-effort output
	}

	return w.Flush()
}

// showBackendDetail displays detailed information about a single backend.
// Descriptor fields supply the operational metadata; backendTradeoffs is
// the CLI-only selling-pitch bullet list (kept separate per round-7 critique).
func showBackendDetail(cmd *cobra.Command, name string) error {
	var desc yoloai.BackendInfo
	found := false
	for _, b := range cliutil.NewSystemClient().Backends(cmd.Context(), yoloai.BackendQuery{ProbeAvailability: true}) {
		if string(b.Name) == name {
			desc = b
			found = true
			break
		}
	}
	if !found {
		return yoerrors.NewUsageError("unknown backend %q (valid: %s)", name, strings.Join(backendNames(cmd), ", "))
	}
	tradeoffs := backendTradeoffs[name]

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
			"name":         desc.Name,
			"description":  desc.Description,
			"available":    desc.Available,
			"note":         desc.Note,
			"platforms":    desc.Platforms,
			"requires":     desc.Requires,
			"install_hint": desc.InstallHint,
			"tradeoffs":    tradeoffs,
		})
	}

	out := cmd.OutOrStdout()

	avail := "yes"
	if !desc.Available {
		avail = "no"
		if desc.Note != "" {
			avail += " (" + desc.Note + ")"
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

// backendNames returns the names of all registered backends in registration
// order; used in usage-error messages enumerating valid choices.
func backendNames(cmd *cobra.Command) []string {
	backends := cliutil.NewSystemClient().Backends(cmd.Context(), yoloai.BackendQuery{})
	names := make([]string, len(backends))
	for i, b := range backends {
		names[i] = string(b.Name)
	}
	return names
}

func newSystemAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents [name]",
		Short: "List available agents",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return agentNames(cmd), cobra.ShellCompDirectiveNoFileComp
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
	agents := cliutil.NewSystemClient().Agents(yoloai.AgentQuery{})

	if cliutil.JSONEnabled(cmd) {
		type agentJSON struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			PromptMode  string `json:"prompt_mode"`
		}
		var items []agentJSON
		for _, a := range agents {
			items = append(items, agentJSON{
				Name:        a.Name,
				Description: a.Description,
				PromptMode:  a.PromptMode,
			})
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), items)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "AGENT\tDESCRIPTION\tPROMPT MODE") //nolint:errcheck

	for _, a := range agents {
		fmt.Fprintf(w, "%s\t%s\t%s\n", a.Name, a.Description, a.PromptMode) //nolint:errcheck
	}

	return w.Flush()
}

// agentNames returns the sorted names of all shipped agents; used for
// shell completion and usage-error enumerations.
func agentNames(cmd *cobra.Command) []string {
	agents := cliutil.NewSystemClient().Agents(yoloai.AgentQuery{})
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}

// showAgentDetail displays detailed information about a single agent.
func showAgentDetail(cmd *cobra.Command, name string) error {
	var def yoloai.AgentInfo
	found := false
	for _, a := range cliutil.NewSystemClient().Agents(yoloai.AgentQuery{}) {
		if a.Name == name {
			def = a
			found = true
			break
		}
	}
	if !found {
		return yoerrors.NewUsageError("unknown agent %q (valid: %s)", name, strings.Join(agentNames(cmd), ", "))
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
