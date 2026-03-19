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
	"strings"

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
// exists with accessible content, the build is skipped.
//
// Fast path: when Docker runs in containerd-snapshotter mode the image is
// already in containerd's "moby" namespace. EnsureImage marks that namespace
// as shareable, walks the descriptor tree, registers each blob in the yoloai
// namespace via a pure bolt metadata write (no data copy), and sets GC ref
// labels so containerd's garbage collector can trace the full manifest tree.
//
// Slow path: docker save | ctr images import - is used when Docker is not in
// containerd-snapshotter mode, or when the fast path fails verification.
func (r *Runtime) EnsureImage(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	ctx = r.withNamespace(ctx)

	if !force {
		if img, err := r.client.GetImage(ctx, imageRef); err == nil {
			// Image record exists — verify the FULL descriptor tree is accessible.
			// Checking only the root is insufficient: GC can remove child blobs
			// (platform manifests, configs, compressed layers) while leaving the
			// root manifest list entry intact, causing img.Unpack to fail later.
			if err := r.verifyDescriptorTree(ctx, r.client.ContentStore(), img.Target()); err == nil {
				return nil // all blobs accessible
			}
			// One or more blobs missing — fall through to rebuild/reimport.
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
	// blob in the yoloai namespace via a pure bolt metadata write. GC ref
	// labels are set on each parent blob so the garbage collector can trace
	// the full manifest tree and keep all blobs reachable.
	fmt.Fprintln(output, "Linking image into containerd namespace yoloai...") //nolint:errcheck // best-effort output
	if err := r.linkFromDockerNamespace(ctx); err == nil {
		fmt.Fprintln(output, "Image ready.") //nolint:errcheck // best-effort output
		return nil
	}

	// Slow path: docker save | ctr images import -
	// Used when Docker is not in containerd-snapshotter mode, or when the
	// fast path fails verification.
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

	// ctr import stores the image under the full Docker ref
	// (docker.io/library/yoloai-base:latest). Create a short alias so the
	// rest of yoloai can look it up by imageRef ("yoloai-base").
	imgSvc := r.client.ImageService()
	if importedImg, err := imgSvc.Get(ctx, dockerImageRef); err == nil {
		_, cerr := imgSvc.Create(ctx, images.Image{Name: imageRef, Target: importedImg.Target})
		if cerr != nil && !errdefs.IsAlreadyExists(cerr) {
			return fmt.Errorf("create image alias: %w", cerr)
		}
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
//     After sharing, GC ref labels (containerd.io/gc.ref.content.*) are set
//     on each parent blob so containerd's garbage collector can trace from
//     the image record through the full manifest tree to all leaf blobs.
//
//  3. Create the image record in yoloai namespace and verify the root
//     descriptor is readable. If the content is not accessible (e.g. GC
//     removed the bolt entries or Docker didn't store compressed blobs in
//     the content store), an error is returned so the caller can fall back
//     to the slow import path.
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
		// Also sets GC ref labels so GC can keep all blobs reachable.
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

		// Verify the FULL descriptor tree is accessible — root, platform
		// manifests, configs, and compressed layer blobs. If any blob is
		// missing (BuildKit didn't materialise it as a separate content
		// object, or GC removed it), bail out so the caller falls back to
		// the slow import path.
		if err := r.verifyDescriptorTree(dstCtx, cs, srcImg.Target); err != nil {
			_ = imgSvc.Delete(dstCtx, imageRef)
			return fmt.Errorf("verify shared content: %w", err)
		}
		return nil
	}
	return fmt.Errorf("image %q not found in Docker containerd namespaces (moby, default)", dockerImageRef)
}

// verifyDescriptorTree recursively confirms that every blob in desc's tree
// has an accessible metadata entry in ctx's namespace. It walks via
// images.Children, so it reads manifest content — if a manifest blob is
// present but a child blob is gone, the walk finds and reports the gap.
// Used both as an early-exit check in EnsureImage and as a post-share
// verification in linkFromDockerNamespace before the slow-path fallback.
func (r *Runtime) verifyDescriptorTree(ctx context.Context, cs content.Store, desc ocispec.Descriptor) error {
	if _, err := cs.Info(ctx, desc.Digest); err != nil {
		return fmt.Errorf("blob %s: %w", desc.Digest, err)
	}
	children, err := images.Children(ctx, cs, desc)
	if err != nil || len(children) == 0 {
		return nil // leaf blob, or manifest not readable (non-fatal for leaf)
	}
	for _, child := range children {
		if err := r.verifyDescriptorTree(ctx, cs, child); err != nil {
			return err
		}
	}
	return nil
}

// shareDescriptorTree walks desc and all its children, registering each blob
// in the destination namespace via a zero-copy metadata-only commit.
// After sharing a parent blob, it sets GC ref labels on it so containerd's
// garbage collector can trace from the image record to all leaf blobs.
func (r *Runtime) shareDescriptorTree(srcCtx, dstCtx context.Context, cs content.Store, desc ocispec.Descriptor) error {
	if err := r.shareBlob(dstCtx, cs, desc); err != nil {
		return err
	}
	children, err := images.Children(srcCtx, cs, desc)
	if err != nil || len(children) == 0 {
		return nil // leaf blob or unreadable — normal for layer/config blobs
	}

	// Set GC ref labels on this parent blob so GC can trace to its children.
	// Without these labels, GC only follows the direct image → root target
	// link and cannot reach manifests, configs, or layers further down.
	if err := r.setGCRefLabels(dstCtx, cs, desc, children); err != nil {
		return fmt.Errorf("set GC labels for %s: %w", desc.Digest, err)
	}

	for _, child := range children {
		if err := r.shareDescriptorTree(srcCtx, dstCtx, cs, child); err != nil {
			return err
		}
	}
	return nil
}

// setGCRefLabels updates the bolt metadata for desc in dstCtx with
// containerd.io/gc.ref.content.* labels pointing to each child descriptor.
// This mirrors what containerd does during image pull so GC can trace the
// full manifest tree from the image record.
func (r *Runtime) setGCRefLabels(ctx context.Context, cs content.Store, desc ocispec.Descriptor, children []ocispec.Descriptor) error {
	info := content.Info{
		Digest: desc.Digest,
		Labels: map[string]string{},
	}
	fields := []string{}
	keys := map[string]uint{}

	for _, child := range children {
		for _, key := range images.ChildGCLabels(child) {
			idx := keys[key]
			keys[key] = idx + 1
			labelKey := key
			if strings.HasSuffix(key, ".sha256.") {
				labelKey = fmt.Sprintf("%s%s", key, child.Digest.Hex()[:12])
			} else if idx > 0 || key[len(key)-1] == '.' {
				labelKey = fmt.Sprintf("%s%d", key, idx)
			}
			info.Labels[labelKey] = child.Digest.String()
			fields = append(fields, "labels."+labelKey)
		}
	}

	if len(fields) == 0 {
		return nil
	}
	_, err := cs.Update(ctx, info, fields...)
	return err
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
