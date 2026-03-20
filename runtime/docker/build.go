// Package docker provides Docker image seeding and building logic for yoloai-base.
// ABOUTME: Handles resource checksums, conflict detection, and build streaming.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/build"
	dockerclient "github.com/docker/docker/client"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// lastBuildFile is the filename used to record the last successful build checksum
// in a profile directory (for profile image staleness detection).
const lastBuildFile = ".last-build-checksum"

// baseImageChecksumPath returns the fixed path where the base image build
// checksum is stored. Using the yoloai cache dir avoids any dependency on
// profiles/base/ or a caller-supplied sourceDir.
func baseImageChecksumPath() string {
	return filepath.Join(config.CacheDir(), ".base-image-checksum")
}

// NeedsBuild returns true if the Docker image needs to be (re)built because
// the embedded resource files have changed since the last successful build.
func NeedsBuild(_ string) bool {
	current := buildInputsChecksum()
	if current == "" {
		return true // shouldn't happen with embedded resources, but be safe
	}
	last, err := os.ReadFile(baseImageChecksumPath()) //nolint:gosec // G304: path is ~/.yoloai/cache/
	if err != nil {
		return true // no record → need build
	}
	return string(last) != current
}

// RecordBuildChecksum writes the current build inputs checksum to disk.
// Exported for testing; production code uses buildBaseImage which records
// automatically on success.
func RecordBuildChecksum(_ string) {
	if sum := buildInputsChecksum(); sum != "" {
		_ = fileutil.WriteFile(baseImageChecksumPath(), []byte(sum), 0600) //nolint:gosec // G304: path is ~/.yoloai/cache/
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
func buildBaseImage(ctx context.Context, client *dockerclient.Client, sourceDir string, output io.Writer, logger *slog.Logger) error {
	buildCtx, err := createBuildContext()
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}

	logger.Debug("building yoloai-base image")

	resp, err := client.ImageBuild(ctx, buildCtx, build.ImageBuildOptions{
		Tags:       []string{"yoloai-base"},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("start image build: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	if err := streamBuildOutput(resp.Body, output); err != nil {
		return err
	}

	// Record build inputs checksum so NeedsBuild can detect stale images.
	RecordBuildChecksum("")

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
		{"sandbox-setup.py", embeddedSandboxSetup},
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

// buildMessage represents a single JSON message from Docker build output.
type buildMessage struct {
	Stream string `json:"stream"`
	Error  string `json:"error"`
}

// BuildProfileImage builds a Docker image from a profile directory's Dockerfile.
// The tag parameter is the full image tag (e.g., "yoloai-go-dev").
// When secrets are provided, the build uses the Docker CLI with BuildKit
// --secret flags instead of the SDK, since BuildKit secret sessions require
// heavy dependencies.
func (r *Runtime) BuildProfileImage(ctx context.Context, sourceDir string, tag string, secrets []string, output io.Writer, logger *slog.Logger) error {
	if len(secrets) > 0 {
		return r.buildProfileImageCLI(ctx, sourceDir, tag, secrets, output, logger)
	}

	buildCtx, err := createProfileBuildContext(sourceDir)
	if err != nil {
		return fmt.Errorf("create profile build context: %w", err)
	}

	logger.Debug("building profile image", "tag", tag, "sourceDir", sourceDir)

	resp, err := r.client.ImageBuild(ctx, buildCtx, build.ImageBuildOptions{
		Tags:       []string{tag},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("start profile image build: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	return streamBuildOutput(resp.Body, output)
}

// buildProfileImageCLI builds a profile image by shelling out to `docker build`
// with BuildKit --secret flags. Used when build secrets are needed.
func (r *Runtime) buildProfileImageCLI(ctx context.Context, sourceDir string, tag string, secrets []string, output io.Writer, logger *slog.Logger) error {
	args := []string{"build", "-t", tag, "-f", "Dockerfile"}
	for _, s := range secrets {
		args = append(args, "--secret", s)
	}
	args = append(args, ".")

	logger.Debug("building profile image via CLI", "tag", tag, "sourceDir", sourceDir, "secrets", len(secrets))

	cmd := exec.CommandContext(ctx, r.binaryName, args...) //nolint:gosec // args are validated by caller
	cmd.Dir = sourceDir
	cmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
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

// streamBuildOutput reads JSON lines from a Docker build response,
// extracts the stream field for human-readable output, and checks for errors.
func streamBuildOutput(response io.Reader, output io.Writer) error {
	decoder := json.NewDecoder(response)
	for {
		var msg buildMessage
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode build output: %w", err)
		}

		if msg.Error != "" {
			return fmt.Errorf("docker build: %s", msg.Error)
		}

		if msg.Stream != "" {
			fmt.Fprint(output, msg.Stream) //nolint:errcheck // best-effort output streaming
		}
	}
}
