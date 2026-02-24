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
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/build"
)

const checksumFile = ".resource-checksums"

// SeedResult describes what happened during resource seeding.
type SeedResult struct {
	Changed         bool     // files were created or updated — image rebuild needed
	Conflicts       []string // files with user customizations that weren't overwritten
	ManifestMissing bool     // checksum manifest was missing or corrupt (first run after upgrade)
}

// SeedResources writes embedded Dockerfile.base and entrypoint.sh to the
// target directory, respecting user customizations.
//
// For each file, the logic is:
//  1. File missing → write it, record checksum.
//  2. File matches last-seeded checksum (unmodified by user) AND embedded
//     version changed → overwrite, update checksum.
//  3. File differs from last-seeded checksum (user customized) AND embedded
//     version changed → write <file>.new, add to Conflicts.
//  4. Embedded version unchanged → nothing to do.
//
// Returns a SeedResult indicating whether a rebuild is needed and whether
// any conflicts were detected.
func SeedResources(targetDir string) (SeedResult, error) {
	var result SeedResult

	if err := os.MkdirAll(targetDir, 0750); err != nil {
		return result, fmt.Errorf("create directory %s: %w", targetDir, err)
	}

	files := []struct {
		name    string
		content []byte
	}{
		{"Dockerfile.base", embeddedDockerfile},
		{"entrypoint.sh", embeddedEntrypoint},
	}

	checksums, manifestOK := loadChecksums(targetDir)
	result.ManifestMissing = !manifestOK

	for _, f := range files {
		path := filepath.Join(targetDir, f.name)
		embeddedSum := sha256Hex(f.content)

		existing, readErr := os.ReadFile(path) //nolint:gosec // G304: targetDir is ~/.yoloai/, not user input

		if readErr != nil {
			// File missing → write it
			if err := os.WriteFile(path, f.content, 0600); err != nil {
				return result, fmt.Errorf("write %s: %w", f.name, err)
			}
			checksums[f.name] = embeddedSum
			result.Changed = true
			continue
		}

		existingSum := sha256Hex(existing)

		if existingSum == embeddedSum {
			// On-disk matches embedded — ensure checksum is recorded
			checksums[f.name] = embeddedSum
			continue
		}

		// Content differs. Was the file modified by the user?
		lastSeeded, hasRecord := checksums[f.name]
		userModified := hasRecord && existingSum != lastSeeded

		// No checksum record (pre-manifest upgrade): conservatively assume
		// user customization since the content differs from embedded.
		if !hasRecord {
			userModified = true
		}

		if userModified {
			// User customized — don't overwrite, write .new for them to review
			newPath := path + ".new"
			if err := os.WriteFile(newPath, f.content, 0600); err != nil {
				return result, fmt.Errorf("write %s: %w", f.name+".new", err)
			}
			result.Conflicts = append(result.Conflicts, f.name)
		} else {
			// Not user-modified (matches last-seeded) — safe to overwrite
			if err := os.WriteFile(path, f.content, 0600); err != nil {
				return result, fmt.Errorf("write %s: %w", f.name, err)
			}
			checksums[f.name] = embeddedSum
			result.Changed = true
		}
	}

	if err := saveChecksums(targetDir, checksums); err != nil {
		return result, fmt.Errorf("save resource checksums: %w", err)
	}

	return result, nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// loadChecksums reads the checksum manifest. Returns the map and true if the
// manifest was loaded successfully, or an empty map and false if it was missing
// or corrupt.
func loadChecksums(dir string) (map[string]string, bool) {
	path := filepath.Join(dir, checksumFile)
	data, err := os.ReadFile(path) //nolint:gosec // G304: dir is ~/.yoloai/
	if err != nil {
		return make(map[string]string), false
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return make(map[string]string), false
	}
	return m, true
}

func saveChecksums(dir string, checksums map[string]string) error {
	data, err := json.MarshalIndent(checksums, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, checksumFile), data, 0600)
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
