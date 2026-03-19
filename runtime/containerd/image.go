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

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/labels"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const imageRef = "yoloai-base"

// dockerImageRef is the full ref Docker uses when storing yoloai-base in containerd.
const dockerImageRef = "docker.io/library/yoloai-base:latest"

// EnsureImage builds the yoloai-base image using Docker and imports it into
// the containerd yoloai namespace. If force is false and the image already
// exists, the build is skipped.
//
// When Docker runs in containerd-snapshotter mode (the default on this VM),
// the image is already in containerd's "moby" namespace after docker build.
// EnsureImage marks that namespace as shareable, then walks the image's
// descriptor tree and registers each blob in the yoloai namespace via a
// pure bolt metadata operation — no physical data is copied. Otherwise it
// falls back to docker save | ctr images import -.
func (r *Runtime) EnsureImage(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	ctx = r.withNamespace(ctx)

	if !force {
		if img, err := r.client.GetImage(ctx, imageRef); err == nil {
			// Image record exists — verify the root content is accessible.
			// If a previous run shared metadata but GC later removed the bolt
			// entries, the image record survives but container creation fails.
			if _, cerr := r.client.ContentStore().Info(ctx, img.Target().Digest); cerr == nil {
				return nil // exists and content is accessible
			}
			// Content missing — fall through to rebuild/reimport.
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
	// directly in containerd (namespace "moby"). Mark that namespace as
	// shareable and walk the image descriptor tree — containerd registers each
	// blob in the yoloai namespace via a pure bolt metadata write. No physical
	// data is copied.
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

// linkFromDockerNamespace registers the yoloai-base image in the yoloai
// containerd namespace by sharing content from Docker's namespace ("moby" or
// "default"). It works in three steps:
//
//  1. Mark the source namespace as shareable
//     (label containerd.io/namespace.shareable=true). This tells containerd
//     that blobs in that namespace may be referenced from other namespaces.
//
//  2. Walk the image's descriptor tree and for each blob call cs.Writer then
//     w.Commit without writing any data. When containerd sees that the blob
//     is in a shareable namespace it takes the shared path: no underlying
//     file writer is created and Commit only writes a bolt metadata entry in
//     the yoloai namespace. No physical data is copied.
//
//  3. Create the image record in yoloai namespace and verify the root
//     descriptor is readable. If the content is not accessible (e.g. GC
//     removed the bolt entries during a brief unreferenced window), an error
//     is returned so the caller can fall back to the slow import path.
func (r *Runtime) linkFromDockerNamespace(ctx context.Context) error {
	imgSvc := r.client.ImageService()
	cs := r.client.ContentStore()
	nsSvc := r.client.NamespaceService()

	// ctx already carries the yoloai namespace (set by EnsureImage).
	// For namespace management and source-namespace lookups use a plain ctx.
	baseCtx := context.Background()

	for _, srcNS := range []string{"moby", "default"} {
		srcCtx := namespaces.WithNamespace(baseCtx, srcNS)
		srcImg, err := imgSvc.Get(srcCtx, dockerImageRef)
		if err != nil {
			continue
		}

		// Mark source namespace as shareable so containerd allows
		// cross-namespace content references without data movement.
		if err := nsSvc.SetLabel(baseCtx, srcNS, labels.LabelSharedNamespace, "true"); err != nil {
			return fmt.Errorf("mark %s namespace as shareable: %w", srcNS, err)
		}

		dstCtx := r.withNamespace(baseCtx)

		// Walk the descriptor tree and register each blob in yoloai.
		if err := r.shareDescriptorTree(srcCtx, dstCtx, cs, srcImg.Target); err != nil {
			return fmt.Errorf("share content: %w", err)
		}

		// Create or update the image record in yoloai namespace.
		// We create first (before any delete) so the content entries are always
		// referenced by an image, preventing GC from removing them.
		_, err = imgSvc.Create(dstCtx, images.Image{
			Name:   imageRef,
			Target: srcImg.Target,
		})
		if err != nil {
			if !errdefs.IsAlreadyExists(err) {
				return fmt.Errorf("create image record: %w", err)
			}
			// Already exists — update in place so the reference is never dropped.
			_, err = imgSvc.Update(dstCtx, images.Image{
				Name:   imageRef,
				Target: srcImg.Target,
			})
			if err != nil {
				return fmt.Errorf("update image record: %w", err)
			}
		}

		// Verify the root descriptor is actually readable in our namespace.
		// If sharing failed silently (e.g. isSharedContent returned false and
		// the underlying writer path produced an ingest that was never flushed),
		// bail out here so the caller can fall back to the slow import path.
		if _, err := cs.Info(dstCtx, srcImg.Target.Digest); err != nil {
			_ = imgSvc.Delete(dstCtx, imageRef)
			return fmt.Errorf("verify shared content: %w", err)
		}
		return nil
	}
	return fmt.Errorf("image %q not found in Docker containerd namespaces (moby, default)", dockerImageRef)
}

// shareDescriptorTree walks desc and all its children, registering each blob
// in the destination namespace via a zero-copy metadata-only commit.
func (r *Runtime) shareDescriptorTree(srcCtx, dstCtx context.Context, cs content.Store, desc ocispec.Descriptor) error {
	if err := r.shareBlob(dstCtx, cs, desc); err != nil {
		return err
	}
	children, err := images.Children(srcCtx, cs, desc)
	if err != nil {
		return nil // blobs have no children — normal for leaf nodes
	}
	for _, child := range children {
		if err := r.shareDescriptorTree(srcCtx, dstCtx, cs, child); err != nil {
			return err
		}
	}
	return nil
}

// shareBlob registers a single content blob in the destination namespace.
// When the source namespace is marked shareable, containerd detects the
// existing blob and performs only a bolt metadata write — no file I/O.
func (r *Runtime) shareBlob(ctx context.Context, cs content.Store, desc ocispec.Descriptor) error {
	w, err := cs.Writer(ctx,
		content.WithRef("yoloai-share-"+desc.Digest.Encoded()),
		content.WithDescriptor(desc),
	)
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			return nil // already in this namespace
		}
		return fmt.Errorf("open writer for %s: %w", desc.Digest, err)
	}
	defer w.Close()

	// Commit without writing — for shared blobs containerd creates only the
	// bolt metadata entry in the destination namespace.
	if err := w.Commit(ctx, desc.Size, desc.Digest); err != nil {
		if errdefs.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("commit %s: %w", desc.Digest, err)
	}
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
