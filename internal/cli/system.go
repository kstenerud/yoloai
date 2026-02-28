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
		newSystemSetupCmd(),
	)

	return cmd
}

func newSystemBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [profile]",
		Short: "Build or rebuild base image",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("profiles not yet implemented")
			}

			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("get home directory: %w", err)
			}
			yoloaiDir := filepath.Join(homeDir, ".yoloai")

			backend := resolveBackend(cmd)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				if err := rt.EnsureImage(ctx, yoloaiDir, os.Stderr, slog.Default(), true); err != nil {
					return err
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
