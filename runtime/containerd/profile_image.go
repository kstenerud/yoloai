//go:build linux

package containerdrt

// ABOUTME: containerd's ProfileImageBuilder implementation (DF153). Profile
// Dockerfiles are built by docker and then imported into the yoloai containerd
// namespace by the same two-path pipeline the base image uses.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

var _ runtime.ProfileImageBuilder = (*Runtime)(nil)
var _ runtime.ImagePresenceChecker = (*Runtime)(nil)

// BuildProfileImage builds a profile's Dockerfile and makes the result usable by
// the containerd backend (DF153). Before this, containerd cleared the CapAdd gate
// in EnsureProfileImage and then fell through the ProfileImageBuilder assertion,
// so a profile's Dockerfile was silently ignored and every sandbox on that profile
// ran an unmodified yoloai-base.
//
// containerd has no builder of its own. Its base image is produced by shelling out
// to `docker build` and then importing the result into the yoloai namespace, and a
// profile image takes exactly the same route — which is why the pipeline that route
// uses is parameterised by tag rather than duplicated here. Two consequences worth
// stating, because neither is obvious from the call:
//
//   - The image is built into *docker's* store and only then linked or imported
//     into containerd's. The build is docker's; the store the sandbox runs from is
//     containerd's, which is why the recorded backend key is "containerd".
//   - `FROM yoloai-base` resolves against docker's store, where Setup has already
//     put it. That is the same coupling the base path has, not a new one.
//
// Unlike apple, containerd gets BuildKit `--secret` support for free, since the
// build is a real `docker build`.
func (r *Runtime) BuildProfileImage(ctx context.Context, sourceDir string, tag string, secrets []string, buildEnv config.Layout, output io.Writer, logger *slog.Logger) error {
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker is required to build profile images for the containerd backend\n" +
			"  Install Docker: https://docs.docker.com/get-docker/")
	}

	dir, err := buildEnv.MkdirTemp("yoloai-containerd-profile-build-")
	if err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}
	defer os.RemoveAll(dir) //nolint:errcheck // best-effort temp cleanup

	if err := dockerrt.WriteProfileBuildContextDir(sourceDir, dir); err != nil {
		return fmt.Errorf("write profile build context: %w", err)
	}

	fmt.Fprintf(output, "Building profile image %s with Docker...\n", tag) //nolint:errcheck // best-effort progress
	logger.Info("building profile image via docker for containerd", "tag", tag, "sourceDir", sourceDir, "context", dir)

	// buildEnv is the env the build subprocess draws from, never r.execEnv
	// captured at construction (the ProfileImageBuilder contract, §12).
	// EnvForDockerBuild also forces BuildKit on, which the containerd image store
	// needs — the legacy builder commits a dangling intermediate image per
	// Dockerfile step there (backend-idiosyncrasies.md).
	// Opt out of BuildKit attestations, for the reason the docker backend states:
	// the attestation manifest index is the prime suspect for images vanishing
	// between runs on a containerd image store, and a local image has no use for
	// attestations. Keeping the flag here means both build paths that feed this
	// backend emit the same shape.
	//
	// It is NOT a speed fix, though it looked like one: the first measurement
	// paired flags-on with a fast namespace link and flags-off with a 76s import,
	// and a control run showed flags-off is also fast. What actually governs the
	// link is whether the layer blobs exist as content objects, which an earlier
	// `docker save` materialises as a side effect.
	args := append([]string{"build"}, dockerrt.AttestationOptOutFlags("docker")...)
	args = append(args, "-t", tag)
	for _, s := range secrets {
		args = append(args, "--secret", s)
	}
	args = append(args, dir)

	buildCmd := sysexec.CommandContext(ctx, buildEnv.Env().EnvForDockerBuild(), dockerBin, args...)
	// Tee into a tail buffer so a failure's cause rides on the error and not only
	// on a stream the caller may discard (DF144/DF145).
	tail := sysexec.NewTailBuffer(buildErrorTailLines)
	w := io.MultiWriter(output, tail)
	buildCmd.Stdout = w
	buildCmd.Stderr = w
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w%s", err, tail.ErrorSuffix())
	}

	return r.importFromDocker(ctx, dockerBin, tag, output)
}

// importFromDocker makes a docker-built tag available in the yoloai containerd
// namespace, trying the zero-copy namespace link first and falling back to
// `docker save | ctr import`. Shared with Setup's base-image path — the two must
// not drift, since a profile image that is present for docker and absent for
// containerd fails at run with a pull of a local-only tag (DF154).
func (r *Runtime) importFromDocker(ctx context.Context, dockerBin string, tag string, output io.Writer) error {
	nsCtx := r.withNamespace(ctx)
	if r.tryLink(nsCtx, tag, output) {
		return nil
	}
	return r.slowPathImport(nsCtx, dockerBin, tag, output)
}

// ProfileImageNeedsBuild reports whether the profile image is stale for
// containerd's own image store, via the shared checksum scheme keyed
// "containerd" (DF150). The key matters: the build happens in docker's store but
// the sandbox runs from containerd's, and a marker written by the docker backend
// must not vouch for an image containerd never received.
func (r *Runtime) ProfileImageNeedsBuild(profileDir string, parentDir string) bool {
	return dockerrt.ProfileImageNeedsBuild(profileDir, parentDir, "containerd")
}

// RecordProfileBuildChecksum records the profile's Dockerfile checksum against
// containerd's store after a successful build. See ProfileImageNeedsBuild.
func (r *Runtime) RecordProfileBuildChecksum(profileDir string) {
	dockerrt.RecordProfileBuildChecksum(profileDir, "containerd")
}

// ImageExists reports whether tag resolves in the yoloai containerd namespace
// with its full descriptor tree accessible, implementing
// runtime.ImagePresenceChecker.
//
// The tree check is not pedantry here: containerd's GC can evict child blobs
// while leaving the root manifest entry intact, so an image can be "present" by
// name and unusable in fact. `imageAlreadyReady` is the same predicate Setup
// trusts before skipping a build, so presence means the same thing to both.
func (r *Runtime) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	return r.imageAlreadyReady(r.withNamespace(ctx), imageRef, false), nil
}
