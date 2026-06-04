package profile

// ABOUTME: `yoloai profile` command group: create, list, info, delete.
// ABOUTME: Manages reusable environment profiles in ~/.yoloai/profiles/.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/spf13/cobra"
)

func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Short:   "Manage profiles",
		GroupID: cliutil.GroupAdmin,
	}

	cmd.AddCommand(
		newProfileCreateCmd(),
		newProfileListCmd(),
		newProfileInfoCmd(),
		newProfileDeleteCmd(),
	)

	return cmd
}

func newProfileCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := cliutil.System().Profiles().Create(cmd.Context(), name); err != nil {
				return err
			}
			yamlPath := filepath.Join(cliutil.Layout().ProfileDir(name), "config.yaml")
			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
					"name":   name,
					"path":   yamlPath,
					"action": "created",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created profile '%s' at %s\n", name, yamlPath) //nolint:errcheck
			return nil
		},
	}
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			summaries, err := cliutil.System().Profiles().List(cmd.Context())
			if err != nil {
				return err
			}
			if len(summaries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No profiles found") //nolint:errcheck
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tIMAGE\tAGENT") //nolint:errcheck
			for _, s := range summaries {
				image := "no"
				if s.HasDockerfile {
					image = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, image, s.AgentType) //nolint:errcheck
			}
			return w.Flush()
		},
	}
}

func newProfileInfoCmd() *cobra.Command {
	var diffMode bool
	cmd := &cobra.Command{
		Use:   "info <name>",
		Short: "Show profile configuration",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			names, err := config.ListProfiles(cliutil.Layout())
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			// Include "base" in completions
			names = append([]string{"base"}, names...)
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProfileInfoCmd(cmd, args[0], diffMode)
		},
	}
	cmd.Flags().BoolVar(&diffMode, "diff", false, "Show only changes from parent profile")
	return cmd
}

func runProfileInfoCmd(cmd *cobra.Command, name string, diffMode bool) error {
	info, err := cliutil.System().Profiles().Info(cmd.Context(), name)
	if err != nil {
		return err
	}

	if diffMode {
		return renderProfileInfoDiff(cmd, info)
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), profileInfoJSON{
			Profile:     info.Name,
			Chain:       info.Chain,
			Image:       info.Image,
			Dockerfile:  info.HasDockerfile,
			Agent:       info.Merged.Agent,
			Model:       info.Merged.Model,
			Backend:     info.Merged.Backend,
			TartImage:   info.Merged.TartImage,
			Isolation:   info.Merged.Isolation,
			Env:         info.Merged.Env,
			AgentArgs:   info.Merged.AgentArgs,
			Ports:       info.Merged.Ports,
			Workdir:     info.Merged.Workdir,
			Directories: info.Merged.Directories,
			Resources:   info.Merged.Resources,
			Network:     info.Merged.Network,
			Mounts:      info.Merged.Mounts,
		})
	}

	return printProfileInfo(cmd, info.Name, "", info.Chain, info.Image, info.HasDockerfile, info.Merged)
}

// renderProfileInfoDiff renders --diff mode output from a ProfileInfo.
// The library has already computed both Merged and Parent; this just
// chooses the output format.
func renderProfileInfoDiff(cmd *cobra.Command, info *yoloai.ProfileInfo) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), profileDiffJSON{
			Profile:   info.Name,
			Chain:     info.Chain,
			Inherited: info.Parent,
			Merged:    info.Merged,
		})
	}

	return printProfileDiff(cmd, info.Name, "", info.Chain, info.Parent, info.Merged)
}

// profileInfoJSON is the JSON output structure for `profile info`.
type profileInfoJSON struct {
	Profile     string                   `json:"profile"`
	Chain       []string                 `json:"chain"`
	Image       string                   `json:"image"`
	Dockerfile  bool                     `json:"dockerfile"`
	Agent       string                   `json:"agent,omitempty"`
	Model       string                   `json:"model,omitempty"`
	Backend     string                   `json:"backend,omitempty"`
	TartImage   string                   `json:"tart_image,omitempty"`
	Isolation   string                   `json:"isolation,omitempty"`
	Env         map[string]string        `json:"env,omitempty"`
	AgentArgs   map[string]string        `json:"agent_args,omitempty"`
	Ports       []string                 `json:"ports,omitempty"`
	Workdir     *yoloai.ProfileWorkdir   `json:"workdir,omitempty"`
	Directories []yoloai.ProfileAuxDir   `json:"directories,omitempty"`
	Resources   *yoloai.ProfileResources `json:"resources,omitempty"`
	Network     *yoloai.ProfileNetwork   `json:"network,omitempty"`
	Mounts      []string                 `json:"mounts,omitempty"`
}

// profileDiffJSON is the JSON output structure for `profile info --diff`.
type profileDiffJSON struct {
	Profile   string                        `json:"profile"`
	Chain     []string                      `json:"chain"`
	Inherited *yoloai.ResolvedProfileConfig `json:"inherited"`
	Merged    *yoloai.ResolvedProfileConfig `json:"merged"`
}

// printProfileInfo renders the human-readable output for `profile info`.
func printProfileInfo(cmd *cobra.Command, name, extends string, chain []string, image string, hasDockerfile bool, merged *yoloai.ResolvedProfileConfig) error {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Profile:     %s\n", name) //nolint:errcheck
	if extends != "" {
		fmt.Fprintf(out, "Extends:     %s\n", extends) //nolint:errcheck
	}
	if len(chain) > 2 {
		fmt.Fprintf(out, "Chain:       %s\n", strings.Join(chain, " \u2192 ")) //nolint:errcheck
	}
	fmt.Fprintf(out, "Image:       %s\n", image) //nolint:errcheck
	dockerfileStr := "no"
	if hasDockerfile {
		dockerfileStr = "yes"
	}
	fmt.Fprintf(out, "Dockerfile:  %s\n", dockerfileStr) //nolint:errcheck

	printProfileInfoScalars(out, merged)
	printProfileInfoMaps(out, merged)
	printProfileInfoDirs(out, merged)
	printProfileInfoResources(out, merged)

	return nil
}

// printProfileInfoScalars prints scalar profile fields.
func printProfileInfoScalars(out io.Writer, merged *yoloai.ResolvedProfileConfig) {
	if merged.Agent != "" {
		fmt.Fprintf(out, "Agent:       %s\n", merged.Agent) //nolint:errcheck
	}
	if merged.Model != "" {
		fmt.Fprintf(out, "Model:       %s\n", merged.Model) //nolint:errcheck
	}
	if merged.Backend != "" {
		fmt.Fprintf(out, "Backend:     %s\n", merged.Backend) //nolint:errcheck
	}
	if merged.TartImage != "" {
		fmt.Fprintf(out, "Tart image:  %s\n", merged.TartImage) //nolint:errcheck
	}
	if merged.Isolation != "" {
		fmt.Fprintf(out, "Isolation:   %s\n", merged.Isolation) //nolint:errcheck
	}
}

// printProfileInfoMaps prints map and list fields (env, agent args, ports, mounts).
func printProfileInfoMaps(out io.Writer, merged *yoloai.ResolvedProfileConfig) {
	if len(merged.Env) > 0 {
		fmt.Fprintln(out, "Env:") //nolint:errcheck
		for _, k := range sortedKeys(merged.Env) {
			fmt.Fprintf(out, "  %s: %s\n", k, merged.Env[k]) //nolint:errcheck
		}
	}
	if len(merged.AgentArgs) > 0 {
		fmt.Fprintln(out, "Agent args:") //nolint:errcheck
		for _, k := range sortedKeys(merged.AgentArgs) {
			fmt.Fprintf(out, "  %s: %s\n", k, merged.AgentArgs[k]) //nolint:errcheck
		}
	}
	if len(merged.Ports) > 0 {
		fmt.Fprintf(out, "Ports:       %s\n", strings.Join(merged.Ports, ", ")) //nolint:errcheck
	}
	if len(merged.Mounts) > 0 {
		fmt.Fprintln(out, "Mounts:") //nolint:errcheck
		for _, m := range merged.Mounts {
			fmt.Fprintf(out, "  %s\n", m) //nolint:errcheck
		}
	}
}

// printProfileInfoDirs prints workdir and directories fields.
func printProfileInfoDirs(out io.Writer, merged *yoloai.ResolvedProfileConfig) {
	if merged.Workdir != nil {
		w := merged.Workdir
		s := w.Path
		if w.Mode != "" {
			s += " (" + w.Mode + ")"
		}
		if w.Mount != "" {
			s += " \u2192 " + w.Mount
		}
		fmt.Fprintf(out, "Workdir:     %s\n", s) //nolint:errcheck
	}
	if len(merged.Directories) > 0 {
		fmt.Fprintln(out, "Directories:") //nolint:errcheck
		for _, d := range merged.Directories {
			s := "  " + d.Path
			if d.Mode != "" {
				s += " (" + d.Mode + ")"
			}
			if d.Mount != "" {
				s += " \u2192 " + d.Mount
			}
			fmt.Fprintln(out, s) //nolint:errcheck
		}
	}
}

// printProfileInfoResources prints resources and network fields.
func printProfileInfoResources(out io.Writer, merged *yoloai.ResolvedProfileConfig) {
	if merged.Resources != nil && (merged.Resources.CPULimit != "" || merged.Resources.MemoryLimit != "") {
		var parts []string
		if merged.Resources.CPULimit != "" {
			parts = append(parts, merged.Resources.CPULimit+" cpus")
		}
		if merged.Resources.MemoryLimit != "" {
			parts = append(parts, merged.Resources.MemoryLimit+" memory")
		}
		fmt.Fprintf(out, "Resources:   %s\n", strings.Join(parts, ", ")) //nolint:errcheck
	}
	if merged.Network != nil && merged.Network.Isolated {
		s := "isolated"
		if len(merged.Network.Allow) > 0 {
			s += " (" + strings.Join(merged.Network.Allow, ", ") + ")"
		}
		fmt.Fprintf(out, "Network:     %s\n", s) //nolint:errcheck
	}
}

// printProfileDiff renders the human-readable diff output for `profile info --diff`.
func printProfileDiff(cmd *cobra.Command, name, extends string, chain []string, parent, merged *yoloai.ResolvedProfileConfig) error {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Profile:   %s\n", name) //nolint:errcheck
	if extends != "" {
		fmt.Fprintf(out, "Extends:   %s\n", extends) //nolint:errcheck
	}
	if len(chain) > 1 {
		fmt.Fprintf(out, "Chain:     %s\n", strings.Join(chain, " → ")) //nolint:errcheck
	}

	hasDiff := anyDiff(
		printScalarDiff(out, "Agent", parent.Agent, merged.Agent),
		printScalarDiff(out, "Model", parent.Model, merged.Model),
		printScalarDiff(out, "Backend", parent.Backend, merged.Backend),
		printScalarDiff(out, "Tart image", parent.TartImage, merged.TartImage),
		printScalarDiff(out, "Isolation", parent.Isolation, merged.Isolation),
		printMapDiff(out, "Env", parent.Env, merged.Env),
		printMapDiff(out, "Agent args", parent.AgentArgs, merged.AgentArgs),
		printListAdditions(out, "Ports", parent.Ports, merged.Ports),
		printListAdditions(out, "Mounts", parent.Mounts, merged.Mounts),
		printWorkdirDiff(out, parent.Workdir, merged.Workdir),
		printDirAdditions(out, parent.Directories, merged.Directories),
		printResourcesDiff(out, parent.Resources, merged.Resources),
		printNetworkDiff(out, parent.Network, merged.Network),
	)

	if !hasDiff {
		parentName := "base"
		if extends != "" {
			parentName = extends
		}
		fmt.Fprintf(out, "\nNo changes from %s\n", parentName) //nolint:errcheck
	}

	return nil
}

// anyDiff returns true if any of the provided values is true.
func anyDiff(vals ...bool) bool {
	for _, v := range vals {
		if v {
			return true
		}
	}
	return false
}

// printScalarDiff prints a scalar field diff. Returns true if printed.
func printScalarDiff(out io.Writer, label, old, new string) bool {
	if old == new {
		return false
	}
	if old == "" {
		fmt.Fprintf(out, "  + %-12s %s\n", label+":", new) //nolint:errcheck
	} else {
		fmt.Fprintf(out, "  ~ %-12s %s → %s\n", label+":", old, new) //nolint:errcheck
	}
	return true
}

// printMapDiff prints per-key diff for a map field. Returns true if any diffs printed.
func printMapDiff(out io.Writer, label string, old, new map[string]string) bool {
	if len(new) == 0 {
		return false
	}

	var lines []string
	for _, k := range sortedKeys(new) {
		oldVal, existed := old[k]
		newVal := new[k]
		if existed && oldVal == newVal {
			continue
		}
		if !existed {
			lines = append(lines, fmt.Sprintf("    + %-10s %s", k+":", newVal))
		} else {
			lines = append(lines, fmt.Sprintf("    ~ %-10s %s → %s", k+":", oldVal, newVal))
		}
	}

	if len(lines) == 0 {
		return false
	}

	fmt.Fprintf(out, "  %s:\n", label) //nolint:errcheck
	for _, line := range lines {
		fmt.Fprintln(out, line) //nolint:errcheck
	}
	return true
}

// printListAdditions prints new items in a list field. Returns true if any printed.
func printListAdditions(out io.Writer, label string, old, new []string) bool {
	if len(new) <= len(old) {
		return false
	}

	added := new[len(old):]
	if len(added) == 0 {
		return false
	}

	fmt.Fprintf(out, "  %s:\n", label) //nolint:errcheck
	for _, item := range added {
		fmt.Fprintf(out, "    + %s\n", item) //nolint:errcheck
	}
	return true
}

// printWorkdirDiff prints workdir diff. Returns true if printed.
func printWorkdirDiff(out io.Writer, old, new *yoloai.ProfileWorkdir) bool {
	if new == nil {
		return false
	}
	if old == nil {
		fmt.Fprintf(out, "  + %-12s %s\n", "Workdir:", formatWorkdir(new)) //nolint:errcheck
		return true
	}
	oldStr := formatWorkdir(old)
	newStr := formatWorkdir(new)
	if oldStr == newStr {
		return false
	}
	fmt.Fprintf(out, "  ~ %-12s %s → %s\n", "Workdir:", oldStr, newStr) //nolint:errcheck
	return true
}

// formatWorkdir formats a ProfileWorkdir for display.
func formatWorkdir(w *yoloai.ProfileWorkdir) string {
	s := w.Path
	if w.Mode != "" {
		s += " (" + w.Mode + ")"
	}
	if w.Mount != "" {
		s += " → " + w.Mount
	}
	return s
}

// printDirAdditions prints directory additions. Returns true if any printed.
func printDirAdditions(out io.Writer, old, new []yoloai.ProfileAuxDir) bool {
	if len(new) <= len(old) {
		return false
	}

	added := new[len(old):]
	if len(added) == 0 {
		return false
	}

	fmt.Fprintln(out, "  Directories:") //nolint:errcheck
	for _, d := range added {
		s := d.Path
		if d.Mode != "" {
			s += " (" + d.Mode + ")"
		}
		if d.Mount != "" {
			s += " → " + d.Mount
		}
		fmt.Fprintf(out, "    + %s\n", s) //nolint:errcheck
	}
	return true
}

// printResourcesDiff prints per-field resources diff. Returns true if any printed.
func printResourcesDiff(out io.Writer, old, new *yoloai.ProfileResources) bool {
	if new == nil {
		return false
	}
	if old == nil {
		old = &yoloai.ProfileResources{}
	}

	var lines []string
	if new.CPULimit != old.CPULimit {
		if old.CPULimit == "" {
			lines = append(lines, fmt.Sprintf("    + %-10s %s", "cpus:", new.CPULimit))
		} else {
			lines = append(lines, fmt.Sprintf("    ~ %-10s %s → %s", "cpus:", old.CPULimit, new.CPULimit))
		}
	}
	if new.MemoryLimit != old.MemoryLimit {
		if old.MemoryLimit == "" {
			lines = append(lines, fmt.Sprintf("    + %-10s %s", "memory:", new.MemoryLimit))
		} else {
			lines = append(lines, fmt.Sprintf("    ~ %-10s %s → %s", "memory:", old.MemoryLimit, new.MemoryLimit))
		}
	}

	if len(lines) == 0 {
		return false
	}

	fmt.Fprintln(out, "  Resources:") //nolint:errcheck
	for _, line := range lines {
		fmt.Fprintln(out, line) //nolint:errcheck
	}
	return true
}

// printNetworkDiff prints network config diff. Returns true if any printed.
func printNetworkDiff(out io.Writer, old, new *yoloai.ProfileNetwork) bool {
	if new == nil {
		return false
	}
	if old == nil {
		old = &yoloai.ProfileNetwork{}
	}

	hasDiff := false
	if new.Isolated != old.Isolated {
		fmt.Fprintf(out, "  ~ %-12s %v → %v\n", "Isolated:", old.Isolated, new.Isolated) //nolint:errcheck
		hasDiff = true
	}

	if len(new.Allow) > len(old.Allow) {
		added := new.Allow[len(old.Allow):]
		if len(added) > 0 {
			fmt.Fprintln(out, "  Network allow:") //nolint:errcheck
			for _, domain := range added {
				fmt.Fprintf(out, "    + %s\n", domain) //nolint:errcheck
			}
			hasDiff = true
		}
	}

	return hasDiff
}

// sortedKeys returns the keys of a map in sorted order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func newProfileDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			ctx := cmd.Context()
			sysClient := cliutil.System()
			profiles := sysClient.Profiles()

			// Profiles no longer support inheritance — no dependency check needed.

			// Surface sandboxes that reference this profile before
			// asking the user to confirm. ReferencingSandboxes returns
			// an error only for an unreadable sandboxes dir; per-meta
			// failures are silently dropped (best-effort warning).
			refs, refsErr := profiles.ReferencingSandboxes(ctx, name)
			if refsErr != nil {
				return refsErr
			}
			if len(refs) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %d sandbox(es) reference this profile: %s\n", len(refs), strings.Join(refs, ", ")) //nolint:errcheck
			}

			if !cliutil.EffectiveYes(cmd) {
				confirmed, confirmErr := cliutil.Confirm(ctx, fmt.Sprintf("Delete profile '%s'? [y/N] ", name), os.Stdin, cmd.ErrOrStderr())
				if confirmErr != nil {
					return confirmErr
				}
				if !confirmed {
					return nil
				}
			}

			result, err := profiles.Delete(ctx, name)
			if err != nil {
				return err
			}

			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
					"name":   name,
					"action": "deleted",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted profile '%s'\n", name) //nolint:errcheck
			renderImageCleanupHints(cmd.OutOrStdout(), result.ImageCleanupHints)
			return nil
		},
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}

// renderImageCleanupHints prints the per-backend "image may exist;
// remove it with: …" suggestions. The data (which backends, what
// command) comes from the library via ProfileDeleteResult; the CLI
// only formats it.
func renderImageCleanupHints(w io.Writer, hints []yoloai.ImageCleanupHint) {
	for _, h := range hints {
		fmt.Fprintf(w, "Note: if a %s image '%s' exists, remove it with: %s\n", h.BackendType, h.Image, h.Command) //nolint:errcheck
	}
}
