// Package docker provides Docker image seeding and building logic for yoloai-base.
// ABOUTME: Handles resource checksums, conflict detection, and build streaming.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// lastBuildFile is the filename used to record the last successful build checksum
// in a profile directory (for profile image staleness detection).
const lastBuildFile = ".last-build-checksum"

// baseImageChecksumPath returns the path where the base image build checksum
// is stored under the given layout's cache directory.
func baseImageChecksumPath(layout config.Layout) string {
	return filepath.Join(layout.CacheDir(), ".base-image-checksum")
}

// NeedsBuild returns true if the Docker image needs to be (re)built because
// the embedded resource files have changed since the last successful build.
func NeedsBuild(layout config.Layout, _ string) bool {
	current := buildInputsChecksum()
	if current == "" {
		return true // shouldn't happen with embedded resources, but be safe
	}
	last, err := os.ReadFile(baseImageChecksumPath(layout)) //nolint:gosec // G304: path is DataDir/cache/
	if err != nil {
		return true // no record → need build
	}
	return string(last) != current
}

// RecordBuildChecksum writes the current build inputs checksum to disk.
// Exported for testing; production code uses buildBaseImage which records
// automatically on success.
func RecordBuildChecksum(layout config.Layout, _ string) {
	if sum := buildInputsChecksum(); sum != "" {
		_ = fileutil.WriteFile(baseImageChecksumPath(layout), []byte(sum), 0600) //nolint:gosec // G304: path is DataDir/cache/
	}
}

// buildInputsChecksum computes a combined SHA-256 of the embedded build inputs.
func buildInputsChecksum() string {
	h := sha256.New()
	type namedContent struct {
		name    string
		content []byte
	}
	files := []namedContent{
		{"Dockerfile", embeddedDockerfile},
		{"entrypoint.sh", embeddedEntrypoint},
		{"entrypoint.py", embeddedEntrypointPy},
		{"sandbox-setup.py", embeddedSandboxSetup},
		{"setup_helpers.py", embeddedSetupHelpers},
		{"tmux_io.py", embeddedTmuxIO},
		{"status-monitor.py", embeddedStatusMonitor},
		{"diagnose-idle.sh", embeddedDiagnoseIdle},
		{"tmux.conf", embeddedTmuxConf},
	}
	for _, f := range files {
		h.Write([]byte(f.name))
		h.Write(f.content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildBaseImage builds the yoloai-base Docker image from the embedded
// Dockerfile and entrypoints. Build output is streamed to the provided
// writer (typically os.Stderr for user-visible progress).
// On success, records a checksum of the build inputs so NeedsBuild can
// detect when a rebuild is required.
//
// The build shells out to `<binary> build -` (BuildKit) rather than the moby
// SDK's ImageBuild, which runs the legacy builder. On the containerd image
// store the legacy builder commits a separate untagged image per Dockerfile
// step; those show up as dangling, form the parent chain of yoloai-base, and
// make `system prune` churn one of them off per run forever (see
// backend-idiosyncrasies.md). BuildKit keeps step results in the build cache
// instead, so no dangling intermediate images are produced. The embedded
// context tar is piped to stdin, so no temp dir is needed.
func (r *Runtime) buildBaseImage(ctx context.Context, layout config.Layout, output io.Writer, logger *slog.Logger) error {
	buildCtx, err := createBuildContext()
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}

	logger.Debug("building yoloai-base image via BuildKit")

	cmd := sysexec.CommandContext(ctx, CuratedBuildEnv(layout.Env), r.binaryName, "build", "-t", "yoloai-base", "-")
	cmd.Stdin = buildCtx
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("%s build exited with code %d", r.binaryName, exitErr.ExitCode())
		}
		return fmt.Errorf("%s build: %w", r.binaryName, err)
	}

	// Record build inputs checksum so NeedsBuild can detect stale images.
	RecordBuildChecksum(layout, "")

	return nil
}

// CreateBuildContext creates an in-memory tar archive containing the
// embedded Dockerfile and entrypoints. Exported so other backends (e.g.
// containerd) can pipe it to `docker build -` without duplicating resources.
func CreateBuildContext() (io.Reader, error) {
	return createBuildContext()
}

// createBuildContext creates an in-memory tar archive containing the
// embedded Dockerfile and entrypoints.
func createBuildContext() (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	files := []struct {
		tarName string
		content []byte
	}{
		{"Dockerfile", embeddedDockerfile},
		{"entrypoint.sh", embeddedEntrypoint},
		{"entrypoint.py", embeddedEntrypointPy},
		{"sandbox-setup.py", embeddedSandboxSetup},
		{"setup_helpers.py", embeddedSetupHelpers},
		{"tmux_io.py", embeddedTmuxIO},
		{"status-monitor.py", embeddedStatusMonitor},
		{"diagnose-idle.sh", embeddedDiagnoseIdle},
		{"tmux.conf", embeddedTmuxConf},
	}

	for _, f := range files {
		header := &tar.Header{
			Name:    f.tarName,
			Size:    int64(len(f.content)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", f.tarName, err)
		}
		if _, err := tw.Write(f.content); err != nil {
			return nil, fmt.Errorf("write tar content for %s: %w", f.tarName, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}

	return &buf, nil
}

// BuildProfileImage builds a Docker image from a profile directory's Dockerfile.
// The tag parameter is the full image tag (e.g., "yoloai-go-dev").
//
// The build always uses BuildKit by shelling out to `<binary> build -` (context
// tar on stdin), never the moby SDK's ImageBuild. The SDK runs the legacy
// builder, which on the containerd image store commits a dangling intermediate
// image per Dockerfile step and makes `system prune` churn forever (see
// backend-idiosyncrasies.md). BuildKit also supplies the `--secret` plumbing
// for profiles that need build secrets.
func (r *Runtime) BuildProfileImage(ctx context.Context, sourceDir string, tag string, secrets []string, buildEnv map[string]string, output io.Writer, logger *slog.Logger) error {
	buildCtx, err := createProfileBuildContext(sourceDir)
	if err != nil {
		return fmt.Errorf("create profile build context: %w", err)
	}

	args := []string{"build", "-t", tag}
	for _, s := range secrets {
		args = append(args, "--secret", s)
	}
	args = append(args, "-")

	logger.Debug("building profile image via BuildKit", "tag", tag, "sourceDir", sourceDir, "secrets", len(secrets))

	cmd := sysexec.CommandContext(ctx, CuratedBuildEnv(buildEnv), r.binaryName, args...)
	cmd.Stdin = buildCtx
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("%s build exited with code %d", r.binaryName, exitErr.ExitCode())
		}
		return fmt.Errorf("%s build: %w", r.binaryName, err)
	}
	return nil
}

// buildEnvAllowlist names the host-environment keys the docker/podman build
// subprocess legitimately needs: daemon connection, registry/credential-helper
// config (HOME + DOCKER/CONTAINER config), proxy settings for base-image pulls,
// SSH-agent forwarding, and the rootless/buildx XDG locations. The build child
// receives ONLY these keys (plus DOCKER_BUILDKIT), drawn from the caller's
// threaded env snapshot — never the live process env (§12). A multi-principal
// embedder that omits a needed key (e.g. PATH for credential helpers) will see
// the build fail loudly rather than silently inherit the host's value.
var buildEnvAllowlist = []string{
	"HOME", "PATH",
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG",
	"DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION",
	"CONTAINER_HOST", "CONTAINERS_CONF", "REGISTRY_AUTH_FILE",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "FTP_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "ftp_proxy", "all_proxy",
	"SSH_AUTH_SOCK",
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
	"BUILDX_CONFIG", "BUILDX_BUILDER",
}

// CuratedBuildEnv assembles the build subprocess's environment from the caller's
// threaded host-env snapshot, keeping only the allowlisted keys and always
// forcing BuildKit on. A nil/empty snapshot yields just DOCKER_BUILDKIT=1 — the
// binary is still found (exec resolves it from the parent PATH at construction),
// but the build runs with no inherited host config, which is the intended
// fail-closed behavior for an embedder that supplied no env.
//
// Exported so sibling backends that shell out to `docker build` (the containerd
// backend's Docker-build fallback) reuse the same §12-curated allowlist rather
// than dumping os.Environ().
func CuratedBuildEnv(snapshot map[string]string) []string {
	env := make([]string, 0, len(buildEnvAllowlist)+1)
	for _, key := range buildEnvAllowlist {
		if v, ok := snapshot[key]; ok && v != "" {
			env = append(env, key+"="+v)
		}
	}
	return append(env, "DOCKER_BUILDKIT=1")
}

// ProfileImageNeedsBuild returns true if the profile image needs to be
// (re)built. Checks: no checksum file, profile Dockerfile changed, or
// parent profile was rebuilt more recently.
func (r *Runtime) ProfileImageNeedsBuild(profileDir string, parentDir string) bool {
	current := profileBuildChecksum(profileDir)
	if current == "" {
		return true
	}

	lastPath := filepath.Join(profileDir, lastBuildFile)
	last, err := os.ReadFile(lastPath) //nolint:gosec // G304: profileDir is from profile resolution
	if err != nil {
		return true
	}
	if string(last) != current {
		return true
	}

	// Check if parent was rebuilt after us
	parentLastPath := filepath.Join(parentDir, lastBuildFile)
	parentInfo, parentErr := os.Stat(parentLastPath)
	if parentErr != nil {
		return false // can't check parent, assume ok
	}
	myInfo, myErr := os.Stat(lastPath)
	if myErr != nil {
		return true
	}
	return parentInfo.ModTime().After(myInfo.ModTime())
}

// RecordProfileBuildChecksum writes the current Dockerfile checksum to disk
// for staleness detection.
func (r *Runtime) RecordProfileBuildChecksum(profileDir string) {
	if sum := profileBuildChecksum(profileDir); sum != "" {
		_ = fileutil.WriteFile(filepath.Join(profileDir, lastBuildFile), []byte(sum), 0600)
	}
}

// profileBuildChecksum computes a SHA-256 of the profile's Dockerfile.
func profileBuildChecksum(profileDir string) string {
	data, err := os.ReadFile(filepath.Join(profileDir, "Dockerfile")) //nolint:gosec // G304: profileDir is from profile resolution
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte("Dockerfile"))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// createProfileBuildContext creates a tar archive from all files in the profile
// directory for Docker build context.
func createProfileBuildContext(sourceDir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("read profile dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Skip internal files
		name := e.Name()
		if name == lastBuildFile || name == "config.yaml" {
			continue
		}

		path := filepath.Join(sourceDir, name)
		content, readErr := os.ReadFile(path) //nolint:gosec // G304: sourceDir is from profile resolution
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", name, readErr)
		}

		header := &tar.Header{
			Name:    name,
			Size:    int64(len(content)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			return nil, fmt.Errorf("write tar content for %s: %w", name, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}

	return &buf, nil
}
