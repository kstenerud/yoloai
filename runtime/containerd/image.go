package containerdrt

// ABOUTME: Image management for the containerd backend.
// EnsureImage builds via Docker and imports into the yoloai containerd namespace.
// ImageExists checks for an existing image in that namespace.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"

	"github.com/containerd/errdefs"
)

const imageRef = "yoloai-base"

// EnsureImage builds the yoloai-base image using Docker and imports it into
// the containerd yoloai namespace. If force is false and the image already
// exists, the build is skipped.
//
// The build pipeline uses shell commands to avoid a Go dependency on
// the Docker SDK from this package:
//
//	docker build -t yoloai-base <sourceDir>
//	docker save yoloai-base | ctr -n yoloai images import -
func (r *Runtime) EnsureImage(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	ctx = r.withNamespace(ctx)

	if !force {
		if _, err := r.client.GetImage(ctx, imageRef); err == nil {
			return nil // already exists
		}
	}

	// Verify docker is available.
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker is required to build the yoloai-base image for the containerd backend\n" +
			"  Install Docker: https://docs.docker.com/get-docker/\n" +
			"  Alternatively, import a pre-built image with:\n" +
			"    docker load -i yoloai-base.tar | ctr -n yoloai images import -")
	}

	ctrBin, err := exec.LookPath("ctr")
	if err != nil {
		return fmt.Errorf("ctr (containerd CLI) not found; install containerd:\n  sudo apt install containerd")
	}

	// Build the image with Docker.
	fmt.Fprintln(output, "Building yoloai-base image with Docker (this may take a few minutes)...") //nolint:errcheck // best-effort output
	logger.Info("building yoloai-base image", "sourceDir", sourceDir)

	buildCmd := exec.CommandContext(ctx, dockerBin, "build", "-t", imageRef, sourceDir) //nolint:gosec // G204: sourceDir is a trusted config path
	buildCmd.Stdout = output
	buildCmd.Stderr = output
	buildCmd.Stdin = nil

	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	// Import into containerd via pipe: docker save | ctr images import -
	fmt.Fprintln(output, "Importing image into containerd namespace yoloai...") //nolint:errcheck // best-effort output

	saveCmd := exec.CommandContext(ctx, dockerBin, "save", imageRef)                       //nolint:gosec // G204: imageRef is a constant
	importCmd := exec.CommandContext(ctx, ctrBin, "-n", "yoloai", "images", "import", "-") //nolint:gosec // G204: ctrBin and args are trusted

	importCmd.Stdin, err = saveCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe setup: %w", err)
	}
	importCmd.Stdout = output
	importCmd.Stderr = output

	if err := saveCmd.Start(); err != nil {
		return fmt.Errorf("docker save: %w", err)
	}

	if err := importCmd.Start(); err != nil {
		_ = saveCmd.Wait()
		return fmt.Errorf("ctr import start: %w", err)
	}

	saveErr := saveCmd.Wait()
	importErr := importCmd.Wait()

	if saveErr != nil {
		return fmt.Errorf("docker save: %w", saveErr)
	}
	if importErr != nil {
		return fmt.Errorf("ctr import: %w", importErr)
	}

	fmt.Fprintln(output, "Image imported successfully.") //nolint:errcheck // best-effort output
	return nil
}

// ImageExists checks if the yoloai-base image exists in the containerd yoloai namespace.
func (r *Runtime) ImageExists(ctx context.Context, ref string) (bool, error) {
	ctx = r.withNamespace(ctx)

	_, err := r.client.GetImage(ctx, ref)
	if err == nil {
		return true, nil
	}
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("check image: %w", err)
}
