package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/build"
)

// SeedResources copies embedded Dockerfile.base and entrypoint.sh to the
// target directory if they don't already exist. Called before build to
// ensure user-editable copies are in place.
func SeedResources(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0750); err != nil {
		return fmt.Errorf("create directory %s: %w", targetDir, err)
	}

	files := []struct {
		name    string
		content []byte
	}{
		{"Dockerfile.base", embeddedDockerfile},
		{"entrypoint.sh", embeddedEntrypoint},
	}

	for _, f := range files {
		path := filepath.Join(targetDir, f.name)
		if _, err := os.Stat(path); err == nil {
			continue // file exists, don't overwrite
		}
		if err := os.WriteFile(path, f.content, 0600); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}
	}

	return nil
}

// BuildBaseImage builds the yoloai-base Docker image from the Dockerfile
// and entrypoint in the given directory. Build output is streamed to the
// provided writer (typically os.Stderr for user-visible progress).
func BuildBaseImage(ctx context.Context, client Client, sourceDir string, output io.Writer, logger *slog.Logger) error {
	buildCtx, err := createBuildContext(sourceDir)
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}

	logger.Debug("building yoloai-base image", "sourceDir", sourceDir)

	resp, err := client.ImageBuild(ctx, buildCtx, build.ImageBuildOptions{
		Tags:       []string{"yoloai-base"},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("start image build: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	return streamBuildOutput(resp.Body, output)
}

// createBuildContext creates an in-memory tar archive containing the
// Dockerfile (renamed from Dockerfile.base) and entrypoint.sh from sourceDir.
func createBuildContext(sourceDir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	files := []struct {
		diskName string // filename on disk
		tarName  string // filename in the tar archive
	}{
		{"Dockerfile.base", "Dockerfile"},
		{"entrypoint.sh", "entrypoint.sh"},
	}

	for _, f := range files {
		path := filepath.Join(sourceDir, f.diskName)
		content, err := os.ReadFile(path) //nolint:gosec // G304: sourceDir is ~/.yoloai/, not user-controlled input
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.diskName, err)
		}

		header := &tar.Header{
			Name:    f.tarName,
			Size:    int64(len(content)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", f.tarName, err)
		}
		if _, err := tw.Write(content); err != nil {
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
