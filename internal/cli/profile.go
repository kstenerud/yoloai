package cli

// ABOUTME: `yoloai profile` command group: create, list, info, delete.
// ABOUTME: Manages reusable environment profiles in ~/.yoloai/profiles/.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Short:   "Manage profiles",
		GroupID: groupAdmin,
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
			if err := sandbox.ValidateProfileName(name); err != nil {
				return err
			}

			if sandbox.ProfileExists(name) {
				return fmt.Errorf("profile %q already exists", name)
			}

			dir := sandbox.ProfileDirPath(name)
			if err := os.MkdirAll(dir, 0750); err != nil {
				return fmt.Errorf("create profile directory: %w", err)
			}

			scaffold := `# extends: base    # parent profile (default: base)
# agent: claude
# model: sonnet
# backend: docker   # optional backend constraint
# tart:
#   image: my-vm    # Tart backend only
# ports:
#   - "8080:8080"
# env:
#   MY_VAR: value
# mounts:                    # extra bind mounts (host:container[:ro])
#   - ~/.gitconfig:/home/yoloai/.gitconfig:ro
# network:
#   isolated: true           # enable network isolation
#   allow:                   # domains allowed when isolated
#     - example.com
# workdir:
#   path: ~/my-project
#   mode: copy       # copy or rw
#   mount: /opt/app  # optional custom mount point
# directories:
#   - path: ~/shared-lib
#     mode: rw
#     mount: /usr/local/lib/shared
`
			yamlPath := filepath.Join(dir, "profile.yaml")
			if err := os.WriteFile(yamlPath, []byte(scaffold), 0600); err != nil {
				return fmt.Errorf("write profile.yaml: %w", err)
			}

			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), map[string]string{
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
			names, err := sandbox.ListProfiles()
			if err != nil {
				return err
			}

			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No profiles found") //nolint:errcheck
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tEXTENDS\tIMAGE\tAGENT") //nolint:errcheck
			for _, name := range names {
				profile, loadErr := sandbox.LoadProfile(name)
				extends := "base"
				agent := ""
				image := "no"
				if loadErr == nil {
					if profile.Extends != "" {
						extends = profile.Extends
					}
					agent = profile.Agent
				}
				if sandbox.ProfileHasDockerfile(name) {
					image = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, extends, image, agent) //nolint:errcheck
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
			names, err := sandbox.ListProfiles()
			if err != nil {
				return nil, cobra.ShellCompDirectiveError
			}
			// Include "base" in completions
			names = append([]string{"base"}, names...)
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			var extends string
			var chain []string
			var merged *sandbox.MergedConfig
			var image string
			var hasDockerfile bool

			if name == "base" {
				// Base profile: no extends, no chain resolution needed
				chain = []string{"base"}
				image = "yoloai-base"
				hasDockerfile = sandbox.ProfileHasDockerfile("base")

				baseCfg, err := sandbox.LoadConfig()
				if err != nil {
					return err
				}
				merged, err = sandbox.MergeProfileChain(baseCfg, chain)
				if err != nil {
					return err
				}
			} else {
				if err := sandbox.ValidateProfileName(name); err != nil {
					return err
				}
				if !sandbox.ProfileExists(name) {
					return fmt.Errorf("profile %q does not exist", name)
				}

				rawProfile, err := sandbox.LoadProfile(name)
				if err != nil {
					return err
				}
				extends = rawProfile.Extends

				chain, err = sandbox.ResolveProfileChain(name)
				if err != nil {
					return err
				}

				baseCfg, err := sandbox.LoadConfig()
				if err != nil {
					return err
				}
				merged, err = sandbox.MergeProfileChain(baseCfg, chain)
				if err != nil {
					return err
				}

				image = sandbox.ResolveProfileImage(name, chain)
				hasDockerfile = sandbox.ProfileHasDockerfile(name)
			}

			if diffMode {
				var parentMerged *sandbox.MergedConfig
				if name == "base" {
					parentMerged = &sandbox.MergedConfig{}
				} else {
					parentChain := chain[:len(chain)-1]
					baseCfg, err := sandbox.LoadConfig()
					if err != nil {
						return err
					}
					parentMerged, err = sandbox.MergeProfileChain(baseCfg, parentChain)
					if err != nil {
						return err
					}
				}

				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), profileDiffJSON{
						Profile:   name,
						Extends:   extends,
						Chain:     chain,
						Inherited: parentMerged,
						Merged:    merged,
					})
				}

				return printProfileDiff(cmd, name, extends, chain, parentMerged, merged)
			}

			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), profileInfoJSON{
					Profile:     name,
					Extends:     extends,
					Chain:       chain,
					Image:       image,
					Dockerfile:  hasDockerfile,
					Agent:       merged.Agent,
					Model:       merged.Model,
					Backend:     merged.Backend,
					TartImage:   merged.TartImage,
					Env:         merged.Env,
					AgentArgs:   merged.AgentArgs,
					Ports:       merged.Ports,
					Workdir:     merged.Workdir,
					Directories: merged.Directories,
					Resources:   merged.Resources,
					Network:     merged.Network,
					Mounts:      merged.Mounts,
				})
			}

			return printProfileInfo(cmd, name, extends, chain, image, hasDockerfile, merged)
		},
	}
	cmd.Flags().BoolVar(&diffMode, "diff", false, "Show only changes from parent profile")
	return cmd
}

// profileInfoJSON is the JSON output structure for `profile info`.
type profileInfoJSON struct {
	Profile     string                  `json:"profile"`
	Extends     string                  `json:"extends"`
	Chain       []string                `json:"chain"`
	Image       string                  `json:"image"`
	Dockerfile  bool                    `json:"dockerfile"`
	Agent       string                  `json:"agent,omitempty"`
	Model       string                  `json:"model,omitempty"`
	Backend     string                  `json:"backend,omitempty"`
	TartImage   string                  `json:"tart_image,omitempty"`
	Env         map[string]string       `json:"env,omitempty"`
	AgentArgs   map[string]string       `json:"agent_args,omitempty"`
	Ports       []string                `json:"ports,omitempty"`
	Workdir     *sandbox.ProfileWorkdir `json:"workdir,omitempty"`
	Directories []sandbox.ProfileDir    `json:"directories,omitempty"`
	Resources   *sandbox.ResourceLimits `json:"resources,omitempty"`
	Network     *sandbox.NetworkConfig  `json:"network,omitempty"`
	Mounts      []string                `json:"mounts,omitempty"`
}

// profileDiffJSON is the JSON output structure for `profile info --diff`.
type profileDiffJSON struct {
	Profile   string                `json:"profile"`
	Extends   string                `json:"extends"`
	Chain     []string              `json:"chain"`
	Inherited *sandbox.MergedConfig `json:"inherited"`
	Merged    *sandbox.MergedConfig `json:"merged"`
}

// printProfileInfo renders the human-readable output for `profile info`.
func printProfileInfo(cmd *cobra.Command, name, extends string, chain []string, image string, hasDockerfile bool, merged *sandbox.MergedConfig) error {
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

	if merged.Resources != nil && (merged.Resources.CPUs != "" || merged.Resources.Memory != "") {
		var parts []string
		if merged.Resources.CPUs != "" {
			parts = append(parts, merged.Resources.CPUs+" cpus")
		}
		if merged.Resources.Memory != "" {
			parts = append(parts, merged.Resources.Memory+" memory")
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

	if len(merged.Mounts) > 0 {
		fmt.Fprintln(out, "Mounts:") //nolint:errcheck
		for _, m := range merged.Mounts {
			fmt.Fprintf(out, "  %s\n", m) //nolint:errcheck
		}
	}

	return nil
}

// printProfileDiff renders the human-readable diff output for `profile info --diff`.
func printProfileDiff(cmd *cobra.Command, name, extends string, chain []string, parent, merged *sandbox.MergedConfig) error {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Profile:   %s\n", name) //nolint:errcheck
	if extends != "" {
		fmt.Fprintf(out, "Extends:   %s\n", extends) //nolint:errcheck
	}
	if len(chain) > 1 {
		fmt.Fprintf(out, "Chain:     %s\n", strings.Join(chain, " → ")) //nolint:errcheck
	}

	hasDiff := false

	hasDiff = printScalarDiff(out, "Agent", parent.Agent, merged.Agent) || hasDiff
	hasDiff = printScalarDiff(out, "Model", parent.Model, merged.Model) || hasDiff
	hasDiff = printScalarDiff(out, "Backend", parent.Backend, merged.Backend) || hasDiff
	hasDiff = printScalarDiff(out, "Tart image", parent.TartImage, merged.TartImage) || hasDiff

	if printed := printMapDiff(out, "Env", parent.Env, merged.Env); printed {
		hasDiff = true
	}

	if printed := printMapDiff(out, "Agent args", parent.AgentArgs, merged.AgentArgs); printed {
		hasDiff = true
	}

	if printed := printListAdditions(out, "Ports", parent.Ports, merged.Ports); printed {
		hasDiff = true
	}

	if printed := printListAdditions(out, "Mounts", parent.Mounts, merged.Mounts); printed {
		hasDiff = true
	}

	if printed := printWorkdirDiff(out, parent.Workdir, merged.Workdir); printed {
		hasDiff = true
	}

	if printed := printDirAdditions(out, parent.Directories, merged.Directories); printed {
		hasDiff = true
	}

	if printed := printResourcesDiff(out, parent.Resources, merged.Resources); printed {
		hasDiff = true
	}

	if printed := printNetworkDiff(out, parent.Network, merged.Network); printed {
		hasDiff = true
	}

	if !hasDiff {
		parentName := "base"
		if extends != "" {
			parentName = extends
		}
		fmt.Fprintf(out, "\nNo changes from %s\n", parentName) //nolint:errcheck
	}

	return nil
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
func printWorkdirDiff(out io.Writer, old, new *sandbox.ProfileWorkdir) bool {
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
func formatWorkdir(w *sandbox.ProfileWorkdir) string {
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
func printDirAdditions(out io.Writer, old, new []sandbox.ProfileDir) bool {
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
func printResourcesDiff(out io.Writer, old, new *sandbox.ResourceLimits) bool {
	if new == nil {
		return false
	}
	if old == nil {
		old = &sandbox.ResourceLimits{}
	}

	var lines []string
	if new.CPUs != old.CPUs {
		if old.CPUs == "" {
			lines = append(lines, fmt.Sprintf("    + %-10s %s", "cpus:", new.CPUs))
		} else {
			lines = append(lines, fmt.Sprintf("    ~ %-10s %s → %s", "cpus:", old.CPUs, new.CPUs))
		}
	}
	if new.Memory != old.Memory {
		if old.Memory == "" {
			lines = append(lines, fmt.Sprintf("    + %-10s %s", "memory:", new.Memory))
		} else {
			lines = append(lines, fmt.Sprintf("    ~ %-10s %s → %s", "memory:", old.Memory, new.Memory))
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
func printNetworkDiff(out io.Writer, old, new *sandbox.NetworkConfig) bool {
	if new == nil {
		return false
	}
	if old == nil {
		old = &sandbox.NetworkConfig{}
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
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := sandbox.ValidateProfileName(name); err != nil {
				return err
			}

			if !sandbox.ProfileExists(name) {
				return fmt.Errorf("profile %q does not exist", name)
			}

			// Check if other profiles extend this one
			allProfiles, err := sandbox.ListProfiles()
			if err != nil {
				return err
			}
			var dependents []string
			for _, other := range allProfiles {
				if other == name {
					continue
				}
				profile, loadErr := sandbox.LoadProfile(other)
				if loadErr != nil {
					continue
				}
				if profile.Extends == name {
					dependents = append(dependents, other)
				}
			}
			if len(dependents) > 0 {
				return fmt.Errorf("cannot delete: profile %q is extended by: %s", name, joinNames(dependents))
			}

			// Check if any sandboxes reference this profile
			refs := findSandboxesWithProfile(name)
			if len(refs) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %d sandbox(es) reference this profile: %s\n", len(refs), joinNames(refs)) //nolint:errcheck
			}

			dir := sandbox.ProfileDirPath(name)
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("remove profile directory: %w", err)
			}

			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), map[string]string{
					"name":   name,
					"action": "deleted",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted profile '%s'\n", name)                                                                   //nolint:errcheck
			fmt.Fprintf(cmd.OutOrStdout(), "Note: if a Docker image 'yoloai-%s' exists, remove it with: docker rmi yoloai-%s\n", name, name) //nolint:errcheck
			return nil
		},
	}
}

// findSandboxesWithProfile scans sandbox meta.json files for profile references.
func findSandboxesWithProfile(profileName string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sandboxesDir := filepath.Join(home, ".yoloai", "sandboxes")
	entries, err := os.ReadDir(sandboxesDir)
	if err != nil {
		return nil
	}

	var refs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(sandboxesDir, e.Name(), "meta.json")
		data, readErr := os.ReadFile(metaPath) //nolint:gosec // G304: path is from sandboxes dir
		if readErr != nil {
			continue
		}
		var meta struct {
			Profile string `json:"profile"`
		}
		if json.Unmarshal(data, &meta) == nil && meta.Profile == profileName {
			refs = append(refs, e.Name())
		}
	}
	return refs
}

// joinNames joins strings with ", ".
func joinNames(names []string) string {
	result := ""
	for i, name := range names {
		if i > 0 {
			result += ", "
		}
		result += name
	}
	return result
}
