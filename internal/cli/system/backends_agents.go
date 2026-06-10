package system

// ABOUTME: Backend and agent info commands, plus the shared probe-based
// ABOUTME: checkBackend helper. All static facts come from runtime.Descriptors();
// ABOUTME: only the per-backend tradeoffs (CLI-presentation language) live here.

import (
	"fmt"
	"os"
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
	"orbstack": {
		"Alias for the Docker backend pinned to OrbStack's daemon socket",
		"Fast, low-overhead Docker + Linux VM for macOS",
		"Picking it targets OrbStack even when other Docker providers are also installed",
	},
	"docker-desktop": {
		"Alias for the Docker backend pinned to Docker Desktop's daemon socket",
		"Picking it targets Docker Desktop even when other Docker providers are also installed",
	},
}

// containerSystemBackendRows returns CLI-only listing rows for the docker-VM
// alias selectors (orbstack, docker-desktop). They are not registered backends —
// each is the docker backend pinned to one daemon socket — so they don't appear
// in runtime.Descriptors(); we surface them here so `yoloai system backends`
// shows every container system the user can pick. Availability = the daemon
// socket is present on disk.
func containerSystemBackendRows() []yoloai.BackendInfo {
	home := cliutil.Layout().HomeDir
	rows := make([]yoloai.BackendInfo, 0, len(yoloai.ContainerSystems()))
	for _, id := range yoloai.ContainerSystems() {
		avail, note := containerSystemAvailable(yoloai.ContainerSystemSocket(id, home))
		rows = append(rows, yoloai.BackendInfo{
			Type:        id,
			Description: "Docker, pinned to " + yoloai.ContainerSystemLabel(id),
			Available:   avail,
			Note:        note,
		})
	}
	return rows
}

// containerSystemAvailable reports whether an alias's pinned daemon socket is
// present on disk (a cheap stat, not a dial — mirrors the docker fallback
// prober's sockExists check).
func containerSystemAvailable(dockerHost string) (available bool, note string) {
	path := strings.TrimPrefix(dockerHost, "unix://")
	if path == "" {
		return false, "no home directory to locate the daemon socket"
	}
	if _, err := os.Stat(path); err != nil {
		return false, "daemon socket not found (is it installed and running?)"
	}
	return true, ""
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
	sys, err := cliutil.System()
	if err != nil {
		return err
	}
	backends := sys.BackendTypes(cmd.Context(), yoloai.BackendQuery{ProbeAvailability: true})
	backends = append(backends, containerSystemBackendRows()...)

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
				Name:        string(b.Type),
				Description: b.Description,
				Available:   b.Available,
				Note:        b.Note,
			})
		}
		return cliutil.WriteJSONList(cmd.OutOrStdout(), "backends", items)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "BACKEND\tDESCRIPTION\tAVAILABLE\tNOTE") //nolint:errcheck // best-effort output

	for _, b := range backends {
		avail := "yes"
		if !b.Available {
			avail = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Type, b.Description, avail, b.Note) //nolint:errcheck // best-effort output
	}

	return w.Flush()
}

// showBackendDetail displays detailed information about a single backend.
// Descriptor fields supply the operational metadata; backendTradeoffs is
// the CLI-only selling-pitch bullet list (kept separate per round-7 critique).
func showBackendDetail(cmd *cobra.Command, name string) error {
	sys, err := cliutil.System()
	if err != nil {
		return err
	}
	var desc yoloai.BackendInfo
	found := false
	rows := append(sys.BackendTypes(cmd.Context(), yoloai.BackendQuery{ProbeAvailability: true}), containerSystemBackendRows()...)
	for _, b := range rows {
		if string(b.Type) == name {
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
			"name":         desc.Type,
			"description":  desc.Description,
			"available":    desc.Available,
			"note":         desc.Note,
			"platforms":    cliutil.EmptyIfNil(desc.Platforms),
			"requires":     desc.Requires,
			"install_hint": desc.InstallHint,
			"tradeoffs":    cliutil.EmptyIfNil(tradeoffs),
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

	fmt.Fprintf(out, "Backend:     %s\n", desc.Type)        //nolint:errcheck
	fmt.Fprintf(out, "Description: %s\n", desc.Description) //nolint:errcheck
	fmt.Fprintf(out, "Available:   %s\n", avail)            //nolint:errcheck
	if len(desc.Platforms) > 0 {
		fmt.Fprintf(out, "Platforms:   %s\n", strings.Join(desc.Platforms, ", ")) //nolint:errcheck
	}
	if desc.Requires != "" {
		fmt.Fprintf(out, "Requires:    %s\n", desc.Requires) //nolint:errcheck
	}
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
func backendNames(_ *cobra.Command) []string {
	backends := yoloai.BackendTypes()
	names := make([]string, 0, len(backends)+len(yoloai.ContainerSystems()))
	for _, b := range backends {
		names = append(names, string(b.Type))
	}
	for _, id := range yoloai.ContainerSystems() {
		names = append(names, string(id))
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
	agents := yoloai.AgentTypes(yoloai.AgentQuery{})

	if cliutil.JSONEnabled(cmd) {
		type agentJSON struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			PromptMode  string `json:"prompt_mode"`
		}
		var items []agentJSON
		for _, a := range agents {
			items = append(items, agentJSON{
				Name:        string(a.Type),
				Description: a.Description,
				PromptMode:  a.PromptMode,
			})
		}
		return cliutil.WriteJSONList(cmd.OutOrStdout(), "agents", items)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "AGENT\tDESCRIPTION\tPROMPT MODE") //nolint:errcheck

	for _, a := range agents {
		fmt.Fprintf(w, "%s\t%s\t%s\n", a.Type, a.Description, a.PromptMode) //nolint:errcheck
	}

	return w.Flush()
}

// agentNames returns the sorted names of all shipped agents; used for
// shell completion and usage-error enumerations.
func agentNames(_ *cobra.Command) []string {
	agents := yoloai.AgentTypes(yoloai.AgentQuery{})
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = string(a.Type)
	}
	return names
}

// showAgentDetail displays detailed information about a single agent.
func showAgentDetail(cmd *cobra.Command, name string) error {
	var def yoloai.AgentInfo
	found := false
	for _, a := range yoloai.AgentTypes(yoloai.AgentQuery{}) {
		if string(a.Type) == name {
			def = a
			found = true
			break
		}
	}
	if !found {
		return yoerrors.NewUsageError("unknown agent %q (valid: %s)", name, strings.Join(agentNames(cmd), ", "))
	}

	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Agent:       %s\n", def.Type)        //nolint:errcheck
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
