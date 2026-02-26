package cli

import (
	"context"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// withClient creates a Docker client, calls fn, and ensures cleanup.
func withClient(cmd *cobra.Command, fn func(ctx context.Context, client docker.Client) error) error {
	ctx := cmd.Context()
	client, err := docker.NewClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck // best-effort cleanup
	return fn(ctx, client)
}

// withManager creates a Docker client and sandbox manager, calls fn, and ensures cleanup.
func withManager(cmd *cobra.Command, fn func(ctx context.Context, mgr *sandbox.Manager) error) error {
	return withClient(cmd, func(ctx context.Context, client docker.Client) error {
		mgr := sandbox.NewManager(client, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		return fn(ctx, mgr)
	})
}
