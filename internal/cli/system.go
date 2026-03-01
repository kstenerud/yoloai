package cli

// ABOUTME: `yoloai system` parent command with `build` and `setup` subcommands.
// ABOUTME: Groups system-level admin operations under a single parent command.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSystemCmd(version, commit, date string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "system",
		Short:   "System information and management",
		GroupID: groupInspect,
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
				return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
					buildOut := os.Stderr
					if jsonEnabled(cmd) {
						buildOut, _ = os.Open(os.DevNull)
					}
					if err := sandbox.EnsureProfileImage(ctx, rt, profileName, backend, buildOut, slog.Default(), true); err != nil {
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

	return cmd
}

func newSystemSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Run interactive setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend := resolveBackend(cmd)
			return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
				return mgr.RunSetup(ctx)
			})
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")

	return cmd
}
