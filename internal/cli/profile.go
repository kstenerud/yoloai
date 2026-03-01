package cli

// ABOUTME: `yoloai profile` command group: create, list, delete.
// ABOUTME: Manages reusable environment profiles in ~/.yoloai/profiles/.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
