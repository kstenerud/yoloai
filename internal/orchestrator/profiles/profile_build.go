// ABOUTME: Profile image building: ensures profile Docker images are built in
// ABOUTME: dependency order (base → parent → child) and detects build secrets.
package profiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// ProfileImageBuilder is optionally implemented by backends that support
// building custom images from profile Dockerfiles. The interface is defined in
// `runtime` alongside every other optional backend capability, so that backends
// can compile-time assert it; this alias keeps the name available to the
// orchestrator tier that consumes it.
type ProfileImageBuilder = runtime.ProfileImageBuilder

// EnsureProfileImage ensures that the Docker image for a profile and its
// entire inheritance chain are built and up to date. Non-Docker backends
// are a no-op. If force is true, all images in the chain are rebuilt.
// secrets are Docker BuildKit --secret specs passed to profile image builds.
//
// layout is the DataDir-rooted Layout used to locate the base profile
// directory and for any host-path needs Setup may have (Q-W.5 threads
// it through runtime.Backend.Setup).
func EnsureProfileImage(ctx context.Context, rt runtime.Backend, layout config.Layout, profileName string, secrets []string, output io.Writer, logger *slog.Logger, force bool) error {
	if !rt.Descriptor().Capabilities.CapAdd {
		return nil
	}

	builder, ok := runtime.ProfileImageBuilderOf(rt)
	if !ok {
		return nil
	}

	chain, err := config.ResolveProfileChain(layout, profileName)
	if err != nil {
		return err
	}

	// Ensure base image first
	baseProfileDir := filepath.Join(layout.ProfilesDir(), "base")
	if err := rt.Setup(ctx, layout, baseProfileDir, output, logger, force); err != nil {
		return fmt.Errorf("ensure base image: %w", err)
	}

	// Walk chain from root to leaf, building each profile that has a Dockerfile.
	// parentChecksum threads the ancestor chain; "" at the root, which preserves
	// the pre-label behaviour that a yoloai-base rebuild does NOT invalidate
	// profile images (see DF156 — that gap is real, predates this, and is filed
	// rather than silently closed here).
	parentChecksum := ""
	for _, name := range chain {
		if name == "base" {
			continue
		}

		profileDir := layout.ProfileDir(name)
		if !config.ProfileHasDockerfile(layout, name) {
			// No Dockerfile — skip, but pass along prevDir unchanged
			continue
		}

		tag := config.ProfileImageTag(layout, name)
		// The chain checksum folds the parent's in, so an ancestor's Dockerfile
		// change reaches every descendant without comparing file timestamps.
		want := chainChecksum(profileDir, parentChecksum)
		if force || !imageMatches(ctx, builder, tag, want) {
			fmt.Fprintf(output, "Building profile image %s...\n", tag) //nolint:errcheck // best-effort output
			if err := builder.BuildProfileImage(ctx, profileDir, tag, want, secrets, layout, output, logger); err != nil {
				return fmt.Errorf("build profile image %s: %w", tag, err)
			}
		}

		parentChecksum = want
	}

	return nil
}

// chainChecksum is the profile-image build checksum: this profile's Dockerfile,
// folded together with its parent's checksum.
//
// Chaining is what makes an ancestor's change reach a descendant. The scheme it
// replaces compared the *modification times* of two marker files, which asked
// "was the parent's bookkeeping touched more recently than mine" — a question
// about the filesystem rather than about the images. Folding the parent's value
// in means a parent rebuild changes the child's expected value by construction,
// at any depth, with nothing to keep in sync.
//
// Returns "" when the Dockerfile cannot be read, which the caller treats as
// "cannot vouch" and therefore builds.
func chainChecksum(profileDir, parentChecksum string) string {
	data, err := os.ReadFile(filepath.Join(profileDir, "Dockerfile")) //nolint:gosec // G304: profileDir comes from profile resolution
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte("Dockerfile"))
	h.Write(data)
	h.Write([]byte(parentChecksum))
	return hex.EncodeToString(h.Sum(nil))
}

// imageMatches reports whether the store already holds tag built from want.
//
// Every "no" answer — absent image, no label, unreadable — is the same answer:
// we cannot vouch for it, so build. That collapses what used to be three
// separate questions (is the marker there, does it match, does the image still
// exist) into one, because the label cannot outlive the image it is on.
func imageMatches(ctx context.Context, builder runtime.ProfileImageBuilder, tag, want string) bool {
	if want == "" {
		return false
	}
	got, ok := builder.ProfileImageChecksum(ctx, tag)
	return ok && got == want
}

// AutoBuildSecrets detects well-known credential files on the host and
// returns Docker BuildKit --secret specs for them. Returns nil if nothing
// is detected.
// homeDir is used for ~ expansion; callers derive it from layout.HomeDir.
func AutoBuildSecrets(homeDir string) []string {
	npmrcPath := config.ExpandTilde("~/.npmrc", homeDir)
	if _, err := os.Stat(npmrcPath); err == nil {
		return []string{"id=npmrc,src=" + npmrcPath}
	}
	return nil
}

// ValidateBuildSecret validates a Docker BuildKit --secret spec string.
// The expected format is "id=<name>,src=<path>". Tilde expansion is applied
// to the src= value. Returns the expanded spec or an error.
// homeDir is used for ~ expansion; callers derive it from layout.HomeDir.
func ValidateBuildSecret(spec, homeDir string) (string, error) {
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

	expanded := config.ExpandTilde(src, homeDir)
	if _, err := os.Stat(expanded); err != nil {
		return "", fmt.Errorf("build secret %q: source file not found: %s", spec, expanded)
	}

	return "id=" + id + ",src=" + expanded, nil
}
