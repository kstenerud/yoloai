package cli

// ABOUTME: `yoloai system` parent command with `build` and `setup` subcommands.
// ABOUTME: Groups system-level admin operations under a single parent command.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSystemCmd(version, commit, date string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "system",
		Short:   "System information and management",
		GroupID: groupAdmin,
	}

	cmd.AddCommand(
		newSystemInfoCmd(version, commit, date),
		newSystemAgentsCmd(),
		newSystemBackendsCmd(),
		newSystemBuildCmd(),
		newSystemPruneCmd(),
		newSystemSetupCmd(),
	)

	return cmd
}

func newSystemBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [profile]",
		Short: "Build or rebuild base image (or profile image)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("get home directory: %w", err)
			}

			backend := resolveBackend(cmd)

			secretFlags, _ := cmd.Flags().GetStringSlice("secret")

			if len(args) > 0 {
				// Build a specific profile's image chain
				profileName := args[0]
				if err := sandbox.ValidateProfileName(profileName); err != nil {
					return err
				}
				if !sandbox.ProfileExists(profileName) {
					return fmt.Errorf("profile %q does not exist", profileName)
				}
				if !sandbox.ProfileHasDockerfile(profileName) {
					// Check if any ancestor has a Dockerfile
					chain, chainErr := sandbox.ResolveProfileChain(profileName)
					if chainErr != nil {
						return chainErr
					}
					hasAny := false
					for _, name := range chain {
						if name != "base" && sandbox.ProfileHasDockerfile(name) {
							hasAny = true
							break
						}
					}
					if !hasAny {
						return fmt.Errorf("profile %q has no Dockerfile (and no ancestor does either)", profileName)
					}
				}

				// Validate user-provided secrets and expand tildes
				var secrets []string
				for _, s := range secretFlags {
					expanded, secretErr := sandbox.ValidateBuildSecret(s)
					if secretErr != nil {
						return secretErr
					}
					secrets = append(secrets, expanded)
				}

				// Prepend auto-detected secrets
				secrets = append(sandbox.AutoBuildSecrets(), secrets...)

				return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
					buildOut := os.Stderr
					if jsonEnabled(cmd) {
						buildOut, _ = os.Open(os.DevNull)
					}
					if err := sandbox.EnsureProfileImage(ctx, rt, profileName, backend, secrets, buildOut, slog.Default(), true); err != nil {
						return err
					}
					if jsonEnabled(cmd) {
						return writeJSON(cmd.OutOrStdout(), map[string]string{"action": "built", "profile": profileName})
					}
					_, err := fmt.Fprintf(cmd.OutOrStdout(), "Profile image built successfully\n")
					return err
				})
			}

			// Build base image only
			if len(secretFlags) > 0 {
				return fmt.Errorf("--secret is only supported with profile builds")
			}
			baseProfileDir := filepath.Join(homeDir, ".yoloai", "profiles", "base")
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				buildOut := os.Stderr
				if jsonEnabled(cmd) {
					buildOut, _ = os.Open(os.DevNull)
				}
				if err := rt.EnsureImage(ctx, baseProfileDir, buildOut, slog.Default(), true); err != nil {
					return err
				}

				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), map[string]string{"action": "built"})
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Base image built successfully")
				return err
			})
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().StringSlice("secret", nil, "Build secret (id=<name>,src=<path>); can be repeated")

	return cmd
}

func newSystemSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Run interactive setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend := resolveBackend(cmd)

			agentFlag, _ := cmd.Flags().GetString("agent")
			tmuxConfFlag, _ := cmd.Flags().GetString("tmux-conf")
			backendFlag, _ := cmd.Flags().GetString("backend")

			opts := sandbox.SetupOptions{
				Agent:    agentFlag,
				Backend:  backendFlag,
				TmuxConf: tmuxConfFlag,
			}

			return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
				return mgr.RunSetup(ctx, opts)
			})
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().String("agent", "", "Default agent (skip prompt)")
	cmd.Flags().String("tmux-conf", "", "Tmux config mode: default, default+host, host, none (skip prompt)")

	return cmd
}
