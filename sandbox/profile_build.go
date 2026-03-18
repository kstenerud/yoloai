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
	"strings"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
)

// ProfileImageBuilder is optionally implemented by backends that support
// building custom images from profile Dockerfiles.
type ProfileImageBuilder interface {
	BuildProfileImage(ctx context.Context, sourceDir string, tag string, secrets []string, output io.Writer, logger *slog.Logger) error
	ProfileImageNeedsBuild(profileDir string, parentDir string) bool
	RecordProfileBuildChecksum(profileDir string)
}

// EnsureProfileImage ensures that the Docker image for a profile and its
// entire inheritance chain are built and up to date. Non-Docker backends
// are a no-op. If force is true, all images in the chain are rebuilt.
// secrets are Docker BuildKit --secret specs passed to profile image builds.
func EnsureProfileImage(ctx context.Context, rt runtime.Runtime, profileName string, backend string, secrets []string, output io.Writer, logger *slog.Logger, force bool) error {
	if !backendCaps(backend).CapAdd {
		return nil
	}

	builder, ok := rt.(ProfileImageBuilder)
	if !ok {
		return nil
	}

	chain, err := config.ResolveProfileChain(profileName)
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

		profileDir := config.ProfileDirPath(name)
		if !config.ProfileHasDockerfile(name) {
			// No Dockerfile — skip, but pass along prevDir unchanged
			continue
		}

		tag := "yoloai-" + name
		if force || builder.ProfileImageNeedsBuild(profileDir, prevDir) {
			fmt.Fprintf(output, "Building profile image %s...\n", tag) //nolint:errcheck // best-effort output
			if err := builder.BuildProfileImage(ctx, profileDir, tag, secrets, output, logger); err != nil {
				return fmt.Errorf("build profile image %s: %w", tag, err)
			}
			builder.RecordProfileBuildChecksum(profileDir)
		}

		prevDir = profileDir
	}

	return nil
}

// AutoBuildSecrets detects well-known credential files on the host and
// returns Docker BuildKit --secret specs for them. Returns nil if nothing
// is detected.
func AutoBuildSecrets() []string {
	npmrcPath := ExpandTilde("~/.npmrc")
	if _, err := os.Stat(npmrcPath); err == nil {
		return []string{"id=npmrc,src=" + npmrcPath}
	}
	return nil
}

// ValidateBuildSecret validates a Docker BuildKit --secret spec string.
// The expected format is "id=<name>,src=<path>". Tilde expansion is applied
// to the src= value. Returns the expanded spec or an error.
func ValidateBuildSecret(spec string) (string, error) {
	parts := strings.Split(spec, ",")

	var id, src string
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "id="):
			id = strings.TrimPrefix(p, "id=")
		case strings.HasPrefix(p, "src="):
			src = strings.TrimPrefix(p, "src=")
		}
	}

	if id == "" {
		return "", fmt.Errorf("build secret %q: missing id= field", spec)
	}
	if src == "" {
		return "", fmt.Errorf("build secret %q: missing src= field", spec)
	}

	expanded := config.ExpandTilde(src)
	if _, err := os.Stat(expanded); err != nil {
		return "", fmt.Errorf("build secret %q: source file not found: %s", spec, expanded)
	}

	return "id=" + id + ",src=" + expanded, nil
}
