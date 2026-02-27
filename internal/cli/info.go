package cli

// ABOUTME: `yoloai info` parent command and `info backends` subcommand.
// Shows system information such as available runtime backends.

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// backendInfo describes a runtime backend for display purposes.
type backendInfo struct {
	Name        string
	Description string
	Detail      backendDetail
}

// backendDetail holds extended information shown by `info backends <name>`.
type backendDetail struct {
	Environment  string
	Platforms    string
	Requires     string
	InstallHint  string
	Tradeoffs    []string
}

// knownBackends is the registry of all supported backends.
var knownBackends = []backendInfo{
	{
		Name:        "docker",
		Description: "Linux containers (Docker)",
		Detail: backendDetail{
			Environment: "Linux (Ubuntu-based container)",
			Platforms:   "Linux, macOS (via Docker Desktop), Windows (via WSL2 + Docker Desktop)",
			Requires:    "Docker Engine or Docker Desktop installed and running",
			InstallHint: "https://docs.docker.com/get-docker/",
			Tradeoffs: []string{
				"Most portable — works everywhere Docker runs",
				"Containers are lightweight and fast to create",
				"Always provides a Linux environment, even on macOS/Windows hosts",
				"File I/O on macOS may be slower due to VirtioFS layer",
			},
		},
	},
	{
		Name:        "tart",
		Description: "macOS VMs (Apple Virtualization)",
		Detail: backendDetail{
			Environment: "macOS (full VM via Apple Virtualization framework)",
			Platforms:   "macOS only (Apple Silicon — M1 or later)",
			Requires:    "Tart CLI installed, Apple Silicon Mac",
			InstallHint: "brew install cirruslabs/cli/tart",
			Tradeoffs: []string{
				"Native macOS environment for macOS-specific development (Xcode, Swift, etc.)",
				"Stronger isolation than seatbelt — full VM boundary",
				"Heavier than containers — VMs take longer to create and use more resources",
				"Apple Silicon only — does not work on Intel Macs",
			},
		},
	},
	{
		Name:        "seatbelt",
		Description: "macOS process sandbox (sandbox-exec)",
		Detail: backendDetail{
			Environment: "macOS (native process with restricted filesystem/network access)",
			Platforms:   "macOS (any architecture)",
			Requires:    "macOS (sandbox-exec is built-in)",
			Tradeoffs: []string{
				"Lightest weight — no container or VM overhead, near-instant startup",
				"Runs natively on the host with the same tools already installed",
				"Less isolation than Docker or Tart — process-level sandbox only",
				"sandbox-exec is deprecated by Apple but still functional",
			},
		},
	},
}

// knownBackendsByName provides lookup by name.
var knownBackendsByName = func() map[string]*backendInfo {
	m := make(map[string]*backendInfo, len(knownBackends))
	for i := range knownBackends {
		m[knownBackends[i].Name] = &knownBackends[i]
	}
	return m
}()

func newInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "info",
		Short:   "Show system information",
		GroupID: groupInspect,
	}

	cmd.AddCommand(newInfoBackendsCmd())
	return cmd
}

func newInfoBackendsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backends [name]",
		Short: "List available runtime backends",
		Args:  cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			names := make([]string, len(knownBackends))
			for i, b := range knownBackends {
				names[i] = b.Name
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

// listBackends displays the summary table of all backends.
func listBackends(cmd *cobra.Command) error {
	ctx := cmd.Context()
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "BACKEND\tDESCRIPTION\tAVAILABLE\tNOTE") //nolint:errcheck // best-effort output

	for _, b := range knownBackends {
		available, note := checkBackend(ctx, b.Name)
		avail := "yes"
		if !available {
			avail = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Name, b.Description, avail, note) //nolint:errcheck // best-effort output
	}

	return w.Flush()
}

// showBackendDetail displays detailed information about a single backend.
func showBackendDetail(cmd *cobra.Command, name string) error {
	b, ok := knownBackendsByName[name]
	if !ok {
		names := make([]string, len(knownBackends))
		for i, kb := range knownBackends {
			names[i] = kb.Name
		}
		return sandbox.NewUsageError("unknown backend %q (valid: %s)", name, strings.Join(names, ", "))
	}

	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	d := b.Detail

	available, note := checkBackend(ctx, name)
	avail := "yes"
	if !available {
		avail = "no"
		if note != "" {
			avail += " (" + note + ")"
		}
	}

	fmt.Fprintf(out, "Backend:     %s\n", b.Name)         //nolint:errcheck
	fmt.Fprintf(out, "Description: %s\n", b.Description)  //nolint:errcheck
	fmt.Fprintf(out, "Available:   %s\n", avail)           //nolint:errcheck
	fmt.Fprintf(out, "Environment: %s\n", d.Environment)   //nolint:errcheck
	fmt.Fprintf(out, "Platforms:   %s\n", d.Platforms)      //nolint:errcheck
	fmt.Fprintf(out, "Requires:    %s\n", d.Requires)       //nolint:errcheck
	if d.InstallHint != "" {
		fmt.Fprintf(out, "Install:     %s\n", d.InstallHint) //nolint:errcheck
	}
	fmt.Fprintln(out)                                       //nolint:errcheck
	fmt.Fprintln(out, "Tradeoffs:")                         //nolint:errcheck
	for _, t := range d.Tradeoffs {
		fmt.Fprintf(out, "  - %s\n", t) //nolint:errcheck
	}

	return nil
}

// checkBackend attempts to create a runtime for the given backend name.
// Returns availability and a short note on failure.
func checkBackend(ctx context.Context, name string) (available bool, note string) {
	rt, err := newRuntime(ctx, name)
	if err != nil {
		return false, err.Error()
	}
	rt.Close() //nolint:errcheck // best-effort cleanup
	return true, ""
}
