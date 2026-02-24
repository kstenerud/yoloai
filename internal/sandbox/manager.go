package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/kstenerud/yoloai/internal/docker"
)

const defaultConfigYAML = `# yoloai configuration
# See https://github.com/kstenerud/yoloai for documentation

defaults:
  agent: claude

  mounts:
    - ~/.gitconfig:/home/yoloai/.gitconfig:ro

  ports: []

  resources:
    cpus: 4
    memory: 8g
`

// Manager is the central orchestrator for sandbox operations.
type Manager struct {
	client docker.Client
	logger *slog.Logger
	output io.Writer
}

// NewManager creates a Manager with the given Docker client, logger,
// and output writer for user-facing messages.
func NewManager(client docker.Client, logger *slog.Logger, output io.Writer) *Manager {
	return &Manager{
		client: client,
		logger: logger,
		output: output,
	}
}

// EnsureSetup performs first-run auto-setup. Idempotent — safe to call
// before every sandbox operation.
func (m *Manager) EnsureSetup(ctx context.Context) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	yoloaiDir := filepath.Join(homeDir, ".yoloai")

	// Create directory structure
	for _, sub := range []string{"sandboxes", "profiles", "cache"} {
		dir := filepath.Join(yoloaiDir, sub)
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	// Seed Dockerfile.base and entrypoint.sh
	if err := docker.SeedResources(yoloaiDir); err != nil {
		return fmt.Errorf("seed resources: %w", err)
	}

	// Check if base image exists; build if missing
	exists, err := imageExists(ctx, m.client, "yoloai-base")
	if err != nil {
		return fmt.Errorf("check base image: %w", err)
	}
	if !exists {
		fmt.Fprintln(m.output, "Building base image (first run only, this may take a few minutes)...") //nolint:errcheck // best-effort output
		if err := docker.BuildBaseImage(ctx, m.client, yoloaiDir, m.output, m.logger); err != nil {
			return fmt.Errorf("build base image: %w", err)
		}
	}

	// Write default config.yaml on first run
	configPath := filepath.Join(yoloaiDir, "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0600); err != nil {
			return fmt.Errorf("write config.yaml: %w", err)
		}
		fmt.Fprintln(m.output, "Tip: enable shell completions with 'yoloai completion --help'") //nolint:errcheck // best-effort output
	}

	return nil
}

// imageExists checks if a Docker image with the given tag exists.
func imageExists(ctx context.Context, client docker.Client, tag string) (bool, error) {
	_, _, err := client.ImageInspectWithRaw(ctx, tag)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, err
}
