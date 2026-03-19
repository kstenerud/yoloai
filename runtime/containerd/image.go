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
	"runtime"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
)

const imageRef = "yoloai-base"

// dockerImageRef is the full ref Docker uses when storing yoloai-base in containerd.
const dockerImageRef = "docker.io/library/yoloai-base:latest"

// EnsureImage builds the yoloai-base image using Docker and imports it into
// the containerd yoloai namespace. If force is false and the image already
// exists, the build is skipped.
//
// When Docker runs in containerd-snapshotter mode (common default), the image
// is already in containerd's "moby" namespace after docker build. In that case
// EnsureImage copies only the image record (metadata) to the yoloai namespace —
// no data movement. Otherwise it falls back to docker save | ctr images import -.
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

	// Fast path: Docker running in containerd-snapshotter mode stores images
	// directly in containerd (namespace "moby"). Copy just the image record to
	// our namespace — no data movement required.
	fmt.Fprintln(output, "Linking image into containerd namespace yoloai...") //nolint:errcheck // best-effort output
	if err := r.linkFromDockerNamespace(ctx); err == nil {
		fmt.Fprintln(output, "Image ready.") //nolint:errcheck // best-effort output
		return nil
	}

	// Slow path: docker save | ctr images import -
	// Used when Docker is not in containerd-snapshotter mode.
	ctrBin, err := exec.LookPath("ctr")
	if err != nil {
		var hint string
		switch runtime.GOOS {
		case "linux":
			hint = "  Ubuntu/Debian: sudo apt install containerd\n  RHEL/Fedora:   sudo dnf install containerd"
		default:
			hint = "  containerd requires a Linux host; see https://containerd.io/docs/getting-started/"
		}
		return fmt.Errorf("ctr (containerd CLI) not found; install containerd:\n%s", hint)
	}

	fmt.Fprintln(output, "Importing image into containerd namespace yoloai (this may take a few minutes)...") //nolint:errcheck // best-effort output

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

	// Wait for import first; if it fails early, kill save to avoid a hang
	// while docker save blocks writing to a broken pipe.
	importErr := importCmd.Wait()
	if importErr != nil {
		_ = saveCmd.Process.Kill()
		_ = saveCmd.Wait()
		return fmt.Errorf("ctr import: %w\n  Hint: ensure your user can access /run/containerd/containerd.sock (run with sudo, or configure a containerd group)", importErr)
	}

	if saveErr := saveCmd.Wait(); saveErr != nil {
		return fmt.Errorf("docker save: %w", saveErr)
	}

	fmt.Fprintln(output, "Image imported successfully.") //nolint:errcheck // best-effort output
	return nil
}

// linkFromDockerNamespace copies the yoloai-base image record from Docker's
// containerd namespace ("moby" or "default") to the yoloai namespace. This is
// a metadata-only operation — the content blobs are shared in containerd's
// content store and require no copying.
func (r *Runtime) linkFromDockerNamespace(ctx context.Context) error {
	imgSvc := r.client.ImageService()

	for _, srcNS := range []string{"moby", "default"} {
		srcCtx := namespaces.WithNamespace(ctx, srcNS)
		srcImg, err := imgSvc.Get(srcCtx, dockerImageRef)
		if err != nil {
			continue
		}

		dstCtx := r.withNamespace(ctx)
		// Delete stale record if force-rebuilding.
		_ = imgSvc.Delete(dstCtx, imageRef)
		_, err = imgSvc.Create(dstCtx, images.Image{
			Name:   imageRef,
			Target: srcImg.Target,
		})
		if err != nil && !errdefs.IsAlreadyExists(err) {
			return fmt.Errorf("create image record: %w", err)
		}
		return nil
	}
	return fmt.Errorf("image %q not found in Docker containerd namespaces (moby, default)", dockerImageRef)
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
