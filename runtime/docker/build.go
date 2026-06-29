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

// baseImageChecksumPath returns the path where the base image build checksum is
// stored under the given layout's cache directory, keyed by backend.
//
// The key MUST be per-image-store: docker, podman, containerd, and apple each
// keep the base image in a SEPARATE store, so a single shared marker let whichever
// backend built first satisfy NeedsBuild for all the others — leaving the
// separate-store backends (podman especially) silently running a stale image after
// a resource change (DF56). Keying by backend makes each store's freshness
// independent so every backend rebuilds when its own image is stale.
func baseImageChecksumPath(layout config.Layout, backendKey string) string {
	return filepath.Join(layout.CacheDir(), ".base-image-checksum-"+backendKey)
}

// NeedsBuild returns true if the base image for backendKey needs to be (re)built
// because the embedded resource files have changed since that backend's last
// successful build. backendKey identifies the image store ("docker", "podman",
// "containerd", "apple") — see baseImageChecksumPath for why it must be per-store.
func NeedsBuild(layout config.Layout, backendKey string) bool {
	current := buildInputsChecksum()
	if current == "" {
		return true // shouldn't happen with embedded resources, but be safe
	}
	last, err := os.ReadFile(baseImageChecksumPath(layout, backendKey)) //nolint:gosec // G304: path is DataDir/cache/
	if err != nil {
		return true // no record → need build
	}
	return string(last) != current
}

// RecordBuildChecksum writes the current build inputs checksum to disk for
// backendKey's image store. Exported for testing; production code uses
// buildBaseImage which records automatically on success.
func RecordBuildChecksum(layout config.Layout, backendKey string) {
	if sum := buildInputsChecksum(); sum != "" {
		_ = fileutil.WriteFile(baseImageChecksumPath(layout, backendKey), []byte(sum), 0600) //nolint:gosec // G304: path is DataDir/cache/
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
		{"firewall.py", embeddedFirewallPy},
		{"install-firewall.py", embeddedInstallFirewallPy},
		{"sandbox-setup.py", embeddedSandboxSetup},
		{"setup_helpers.py", embeddedSetupHelpers},
		{"tmux_io.py", embeddedTmuxIO},
		{"status-monitor.py", embeddedStatusMonitor},
		{"diagnose-idle.sh", embeddedDiagnoseIdle},
		{"agent-run.sh", embeddedAgentRun},
		{"yoloai-resume", embeddedYoloaiResume},
		{"tmux.conf", embeddedTmuxConf},
	}
	for _, f := range files {
		h.Write([]byte(f.name))
		h.Write(f.content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// attestationOptOutFlags returns the build flags that disable BuildKit
// SBOM/provenance attestations — but only for docker, which emits them. The
// attestation manifest list is the prime suspect for yoloai-base/profile images
// vanishing between runs on Docker Desktop's containerd image store (forcing a
// full rebuild every time), and a local image has no use for attestations
// anyway; harmless on the classic overlay2 store. Podman's `build` neither emits
// such attestations nor accepts --provenance/--sbom (it errors "unknown flag:
// --provenance"), so the flags are omitted for it.
func attestationOptOutFlags(binaryName string) []string {
	if binaryName == "docker" {
		return []string{"--provenance=false", "--sbom=false"}
	}
	return nil
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

	args := append([]string{"build"}, attestationOptOutFlags(r.binaryName)...)
	args = append(args, "-t", "yoloai-base", "-")
	cmd := sysexec.CommandContext(ctx, layout.Env().EnvForDockerBuild(), r.binaryName, args...)
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
	RecordBuildChecksum(layout, r.binaryName)

	return nil
}

// CreateBuildContext creates an in-memory tar archive containing the
// embedded Dockerfile and entrypoints. Exported so other backends (e.g.
// containerd) can pipe it to `docker build -` without duplicating resources.
func CreateBuildContext() (io.Reader, error) {
	return createBuildContext()
}

// WriteBuildContextDir materializes the same embedded base-image build context
// (Dockerfile, entrypoints, scripts, tmux.conf) into dir. Backends whose build
// command needs a *directory* context rather than a stdin tar — e.g. Apple
// `container build <dir>` — use this instead of CreateBuildContext. It reuses
// createBuildContext as the single source of truth for the file set.
func WriteBuildContextDir(dir string) error {
	tarReader, err := createBuildContext()
	if err != nil {
		return err
	}
	tr := tar.NewReader(tarReader)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read build context tar: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("read %s from build context: %w", hdr.Name, err)
		}
		if err := fileutil.WriteFile(filepath.Join(dir, hdr.Name), data, 0644); err != nil { //nolint:gosec // G306: build-context files, dir is a caller-owned temp dir
			return fmt.Errorf("write %s: %w", hdr.Name, err)
		}
	}
	return nil
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
		{"firewall.py", embeddedFirewallPy},
		{"install-firewall.py", embeddedInstallFirewallPy},
		{"sandbox-setup.py", embeddedSandboxSetup},
		{"setup_helpers.py", embeddedSetupHelpers},
		{"tmux_io.py", embeddedTmuxIO},
		{"status-monitor.py", embeddedStatusMonitor},
		{"diagnose-idle.sh", embeddedDiagnoseIdle},
		{"agent-run.sh", embeddedAgentRun},
		{"yoloai-resume", embeddedYoloaiResume},
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
func (r *Runtime) BuildProfileImage(ctx context.Context, sourceDir string, tag string, secrets []string, buildEnv config.Layout, output io.Writer, logger *slog.Logger) error {
	buildCtx, err := createProfileBuildContext(sourceDir)
	if err != nil {
		return fmt.Errorf("create profile build context: %w", err)
	}

	args := append([]string{"build"}, attestationOptOutFlags(r.binaryName)...)
	args = append(args, "-t", tag)
	for _, s := range secrets {
		args = append(args, "--secret", s)
	}
	args = append(args, "-")

	logger.Debug("building profile image via BuildKit", "tag", tag, "sourceDir", sourceDir, "secrets", len(secrets))

	cmd := sysexec.CommandContext(ctx, buildEnv.Env().EnvForDockerBuild(), r.binaryName, args...)
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
