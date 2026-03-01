package sandbox

// ABOUTME: Profile image building orchestration. Ensures profile images are
// ABOUTME: built in dependency order (base → parent → child).

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// ProfileImageBuilder is optionally implemented by backends that support
// building custom images from profile Dockerfiles.
type ProfileImageBuilder interface {
	BuildProfileImage(ctx context.Context, sourceDir string, tag string, output io.Writer, logger *slog.Logger) error
	ProfileImageNeedsBuild(profileDir string, parentDir string) bool
	RecordProfileBuildChecksum(profileDir string)
}

// EnsureProfileImage ensures that the Docker image for a profile and its
// entire inheritance chain are built and up to date. Non-Docker backends
// are a no-op. If force is true, all images in the chain are rebuilt.
func EnsureProfileImage(ctx context.Context, rt runtime.Runtime, profileName string, backend string, output io.Writer, logger *slog.Logger, force bool) error {
	if backend != "docker" {
		return nil
	}

	builder, ok := rt.(ProfileImageBuilder)
	if !ok {
		return nil
	}

	chain, err := ResolveProfileChain(profileName)
	if err != nil {
		return err
	}

	// Ensure base image first
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	baseProfileDir := filepath.Join(home, ".yoloai", "profiles", "base")
	if err := rt.EnsureImage(ctx, baseProfileDir, output, logger, force); err != nil {
		return fmt.Errorf("ensure base image: %w", err)
	}

	// Walk chain from root to leaf, build each profile that has a Dockerfile
	prevDir := baseProfileDir
	for _, name := range chain {
		if name == "base" {
			continue
		}

		profileDir := ProfileDirPath(name)
		if !ProfileHasDockerfile(name) {
			// No Dockerfile — skip, but pass along prevDir unchanged
			continue
		}

		tag := "yoloai-" + name
		if force || builder.ProfileImageNeedsBuild(profileDir, prevDir) {
			fmt.Fprintf(output, "Building profile image %s...\n", tag) //nolint:errcheck // best-effort output
			if err := builder.BuildProfileImage(ctx, profileDir, tag, output, logger); err != nil {
				return fmt.Errorf("build profile image %s: %w", tag, err)
			}
			builder.RecordProfileBuildChecksum(profileDir)
		}

		prevDir = profileDir
	}

	return nil
}
