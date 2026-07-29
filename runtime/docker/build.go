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
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// lastBuildPrefix is the filename prefix used to record the last successful
// profile build checksum in a profile directory (profile image staleness
// detection). The full name is suffixed with the backend key — see
// profileChecksumPath. The bare prefix is also what the build-context filter
// matches on, so a marker left by any backend (including the pre-DF150
// unkeyed one) stays out of the build context.
const lastBuildPrefix = ".last-build-checksum"

// profileChecksumPath returns the profile build-checksum marker for backendKey.
//
// Keyed for the same reason baseImageChecksumPath is (DF56, DF150): the profile
// directory is shared across backends but the image stores are not, so one
// unkeyed marker let whichever backend built first answer "already built" for
// every other backend — which then skipped a build whose image it did not have
// and failed at `run` trying to pull a local-only tag. It reproduced between
// docker and podman on one Linux host, in both directions.
//
// The same caveat as the base-image marker applies and is NOT yet addressed
// here: a host-side marker keyed by backend name is exact only where one
// backend name means one store. The docker backend can be pointed at any of
// several local daemons (OrbStack, Docker Desktop, Colima), and this marker
// cannot tell them apart — the base image solves that by stamping the checksum
// onto the image itself (baseChecksumLabel) so staleness travels with the
// image. The profile path cannot do that yet: ProfileImageNeedsBuild takes
// neither a context nor a tag, so it cannot inspect an image. See DF152.
func profileChecksumPath(profileDir string, backendKey string) string {
	return filepath.Join(profileDir, lastBuildPrefix+"-"+backendKey)
}

// buildErrorTailLines is how many trailing lines of a failed build's output are
// carried on the returned error (DF144) — enough to include the failing
// BuildKit step and its error, without dumping the whole log into one line.
const buildErrorTailLines = 20

// baseImageChecksumPath returns the path where the base image build checksum is
// stored under the given layout's cache directory, keyed by backend.
//
// The key MUST be per-image-store: docker, podman, containerd, and apple each
// keep the base image in a SEPARATE store, so a single shared marker let whichever
// backend built first satisfy NeedsBuild for all the others — leaving the
// separate-store backends (podman especially) silently running a stale image after
// a resource change (DF56). Keying by backend makes each store's freshness
// independent so every backend rebuilds when its own image is stale.
//
// This host-side marker is correct only for backends that are one store per
// backend name — apple (one host VM-image store) and containerd (one image
// store). The docker backend is NOT: it can be connected to any of several local
// daemons (OrbStack, Docker Desktop, Colima, …), each a separate store, so a
// host-side marker keyed by backend can't tell them apart. The docker runtime
// therefore stamps the checksum onto the image itself (baseChecksumLabel) and
// reads it back via baseImageStale — staleness travels with the image, in its
// store — instead of using this path.
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
	last, err := os.ReadFile(baseImageChecksumPath(layout, backendKey))
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
		_ = fileutil.WriteFile(baseImageChecksumPath(layout, backendKey), []byte(sum), 0600)
	}
}

// baseChecksumLabel is the image label that stamps the build-inputs checksum onto
// yoloai-base, so staleness travels with the image in whatever store holds it.
const baseChecksumLabel = "yoloai.base.checksum"

// checksumLabelStale reports whether an image carrying the given labels is stale
// relative to the current build-inputs checksum. An empty want disables the check
// (treat as fresh); a missing or mismatched label is stale.
func checksumLabelStale(want string, labels map[string]string) bool {
	if want == "" {
		return false
	}
	return labels[baseChecksumLabel] != want
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

// AttestationOptOutFlags returns the build flags that disable BuildKit
// SBOM/provenance attestations — but only for docker, which emits them. The
// attestation manifest list is the prime suspect for yoloai-base/profile images
// vanishing between runs on Docker Desktop's containerd image store (forcing a
// full rebuild every time), and a local image has no use for attestations
// anyway; harmless on the classic overlay2 store. Podman's `build` neither emits
// such attestations nor accepts --provenance/--sbom (it errors "unknown flag:
// --provenance"), so the flags are omitted for it.
func AttestationOptOutFlags(binaryName string) []string {
	if binaryName == "docker" {
		return []string{"--provenance=false", "--sbom=false"}
	}
	return nil
}

// buildBaseImage builds the yoloai-base Docker image from the embedded
// Dockerfile and entrypoints. Build output is streamed to the provided
// writer (typically os.Stderr for user-visible progress).
// On success the image carries a yoloai.base.checksum label of the build
// inputs, so baseImageStale can detect when a rebuild is required by reading
// the label off the image itself (rather than a host-side marker keyed by
// backend, which let a second Docker provider run a stale base — see D107).
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

	args := append([]string{"build"}, AttestationOptOutFlags(r.binaryName)...)
	// Stamp the build-inputs checksum onto the image so baseImageStale can detect
	// a stale yoloai-base per store, without a host-side marker — the docker
	// backend can hold separate images across local providers (OrbStack, Docker
	// Desktop, …). --label does not affect buildInputsChecksum (which hashes the
	// embedded file contents, not build flags), so there is no chicken-and-egg.
	if sum := buildInputsChecksum(); sum != "" {
		args = append(args, "--label", baseChecksumLabel+"="+sum)
	}
	args = append(args, "-t", "yoloai-base", "-")
	cmd := sysexec.CommandContext(ctx, layout.Env().EnvForDockerBuild(), r.binaryName, args...)
	cmd.Stdin = buildCtx
	// Stream to output as before, but also tee into a tail buffer so a failure's
	// actionable cause rides on the error itself, not only the (maybe discarded)
	// stream — same value on both so os/exec keeps its single-pipe path (DF144).
	tail := sysexec.NewTailBuffer(buildErrorTailLines)
	w := io.MultiWriter(output, tail)
	cmd.Stdout = w
	cmd.Stderr = w

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("%s build exited with code %d%s", r.binaryName, exitErr.ExitCode(), tail.ErrorSuffix())
		}
		return fmt.Errorf("%s build: %w%s", r.binaryName, err, tail.ErrorSuffix())
	}

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
	return writeTarToDir(tarReader, dir)
}

// WriteProfileBuildContextDir materializes a profile's build context (its
// Dockerfile and sibling files, excluding the internal checksum marker and
// config.yaml — same filtering as createProfileBuildContext) into dir.
// Backends whose build command needs a *directory* context rather than a
// stdin tar — e.g. Apple `container build <dir>` — use this instead of the
// tar-based profile build context.
func WriteProfileBuildContextDir(sourceDir string, dir string) error {
	tarReader, err := createProfileBuildContext(sourceDir)
	if err != nil {
		return err
	}
	return writeTarToDir(tarReader, dir)
}

// writeTarToDir unpacks a tar stream into dir, one file per entry.
func writeTarToDir(tarReader io.Reader, dir string) error {
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

	args := append([]string{"build"}, AttestationOptOutFlags(r.binaryName)...)
	args = append(args, "-t", tag)
	for _, s := range secrets {
		args = append(args, "--secret", s)
	}
	args = append(args, "-")

	logger.Debug("building profile image via BuildKit", "tag", tag, "sourceDir", sourceDir, "secrets", len(secrets))

	cmd := sysexec.CommandContext(ctx, buildEnv.Env().EnvForDockerBuild(), r.binaryName, args...)
	cmd.Stdin = buildCtx
	tail := sysexec.NewTailBuffer(buildErrorTailLines)
	w := io.MultiWriter(output, tail)
	cmd.Stdout = w
	cmd.Stderr = w

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("%s build exited with code %d%s", r.binaryName, exitErr.ExitCode(), tail.ErrorSuffix())
		}
		return fmt.Errorf("%s build: %w%s", r.binaryName, err, tail.ErrorSuffix())
	}
	return nil
}

// ProfileImageNeedsBuild returns true if the profile image needs to be
// (re)built. Checks: no checksum file, profile Dockerfile changed, or
// parent profile was rebuilt more recently.
func (r *Runtime) ProfileImageNeedsBuild(profileDir string, parentDir string) bool {
	return ProfileImageNeedsBuild(profileDir, parentDir, r.storeKey())
}

// storeKey names the image store this Runtime is actually talking to, for
// keying host-side build markers.
//
// The binary name alone is not it. `orbstack` and `docker-desktop` are
// first-class backend ids that both resolve to this runtime with binaryName
// "docker" and a pinned socket (runtime.ResolveContainerSystem), and podman
// supports remote connections the same way — so one binary name can address
// several daemons, each with its own image store. Keying a "have I built this?"
// marker by the binary name lets a build under one provider answer for another,
// which is DF150's defect one level up: the key was made per-backend but a
// backend is not a store.
//
// DaemonHost() is the endpoint this client was constructed with, so it is a
// local read with no round trip. It is hashed rather than used raw because it is
// a URL and the key becomes a filename; short is enough, since this only has to
// separate the two or three daemons on one host, not be globally unique. Two
// spellings of the same socket (a symlinked path) hash differently and cost one
// extra rebuild, which is the safe direction.
func (r *Runtime) storeKey() string {
	host := r.client.DaemonHost()
	if host == "" {
		return r.binaryName
	}
	sum := sha256.Sum256([]byte(host))
	return r.binaryName + "-" + hex.EncodeToString(sum[:4])
}

// RecordProfileBuildChecksum writes the current Dockerfile checksum to disk
// for staleness detection.
func (r *Runtime) RecordProfileBuildChecksum(profileDir string) {
	RecordProfileBuildChecksum(profileDir, r.storeKey())
}

// ProfileImageNeedsBuild reports whether backendKey's profile image is stale:
// no marker for that backend, the profile Dockerfile changed, or the parent
// profile was rebuilt more recently.
//
// This is the scheme itself rather than a method, because it has more than one
// consumer: the docker Runtime (which passes its own binaryName, so podman gets
// "podman" through the embedding and keeps a separate marker) and any other
// image-based backend that keeps its own store. backendKey names that store —
// "docker", "podman", "containerd", "apple" — exactly as it does for the base
// image in NeedsBuild.
func ProfileImageNeedsBuild(profileDir string, parentDir string, backendKey string) bool {
	current := profileBuildChecksum(profileDir)
	if current == "" {
		return true
	}

	lastPath := profileChecksumPath(profileDir, backendKey)
	last, err := os.ReadFile(lastPath) //nolint:gosec // G304: profileDir is from profile resolution
	if err != nil {
		return true
	}
	if string(last) != current {
		return true
	}

	// Check if parent was rebuilt after us — in THIS backend's store, so the
	// parent marker is read under the same key.
	parentLastPath := profileChecksumPath(parentDir, backendKey)
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

// RecordProfileBuildChecksum records the profile's Dockerfile checksum for
// backendKey's store after a successful build. See ProfileImageNeedsBuild.
func RecordProfileBuildChecksum(profileDir string, backendKey string) {
	if sum := profileBuildChecksum(profileDir); sum != "" {
		_ = fileutil.WriteFile(profileChecksumPath(profileDir, backendKey), []byte(sum), 0600)
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
		// Skip internal files. Prefix-matched so every backend's keyed marker
		// is excluded, along with the pre-DF150 unkeyed one that may still be
		// sitting in a profile dir from an older install.
		name := e.Name()
		if strings.HasPrefix(name, lastBuildPrefix) || name == "config.yaml" {
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
