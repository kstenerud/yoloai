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

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
)

// legacyMarkerPrefix is the filename prefix of the pre-label staleness markers
// (`.last-build-checksum*`). Nothing writes them any more — staleness lives on
// the image as runtime.ProfileChecksumLabel — but installs upgraded from an
// older yoloai still have them sitting in profile directories, and they must
// stay out of build contexts. A filter only; see DF152.
const legacyMarkerPrefix = ".last-build-checksum"

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
// baseChecksumLabel aliases the shared constant so this package's existing
// readers keep their local name while every backend stamps one label.
const baseChecksumLabel = runtime.BaseChecksumLabel

// checksumLabelStale reports whether an image carrying the given labels is stale
// relative to the current build-inputs checksum. An empty want disables the check
// (treat as fresh); a missing or mismatched label is stale.
func checksumLabelStale(want string, labels map[string]string) bool {
	if want == "" {
		return false
	}
	return labels[baseChecksumLabel] != want
}

// buildInput is one embedded file that participates in the base image build.
//
// guestMode is the mode the file needs *in the guest*, matching the Dockerfile's
// own `chmod +x` list. It is unused by the build (the Dockerfile sets final
// permissions itself) and load-bearing only for WriteRuntimeScripts, which
// delivers these files at launch and has no Dockerfile to fix them up
// afterwards.
//
// runtimeBin marks the files the Dockerfile COPYs into /yoloai/bin — the set the
// host process drives by absolute path, and therefore the set that must come from
// the running binary rather than from whatever a profile image inherited (DF156).
// Dockerfile is build-only; tmux.conf lands in /yoloai/tmux and has its own mount.
type buildInput struct {
	name       string
	content    []byte
	guestMode  os.FileMode
	runtimeBin bool
}

// baseBuildInputs is the single source of truth for the embedded file set, shared
// by buildInputsChecksum, createBuildContext and WriteRuntimeScripts. It existed
// as three hand-maintained copies of the same list, which is how a file can enter
// the embed set without entering every consumer of it — the failure DF156's
// sharp case is an instance of.
//
// **Order is load-bearing.** buildInputsChecksum hashes the identity subset in
// slice order, and that checksum is the base image's identity label: reordering
// the entries it selects marks every existing yoloai-base stale and rebuilds it
// once on every host. Append; do not sort.
//
// It still cannot catch a file added to the Dockerfile and not here (nothing
// typechecks a COPY line); TestBaseBuildInputs_MatchTheDockerfilesBinCopies
// closes that half by parsing the Dockerfile.
func baseBuildInputs() []buildInput {
	const exec, data = 0o755, 0o644
	return []buildInput{
		{"Dockerfile", embeddedDockerfile, data, false},
		{"entrypoint.sh", embeddedEntrypoint, exec, true},
		{"entrypoint.py", embeddedEntrypointPy, exec, true},
		{"firewall.py", embeddedFirewallPy, data, true},
		{"install-firewall.py", embeddedInstallFirewallPy, exec, true},
		{"sandbox-setup.py", embeddedSandboxSetup, data, true},
		{"setup_helpers.py", embeddedSetupHelpers, data, true},
		{"tmux_io.py", embeddedTmuxIO, data, true},
		{"status-monitor.py", embeddedStatusMonitor, data, true},
		{"diagnose-idle.sh", embeddedDiagnoseIdle, exec, true},
		{"agent-run.sh", embeddedAgentRun, exec, true},
		{"yoloai-resume", embeddedYoloaiResume, exec, true},
		{"tmux.conf", embeddedTmuxConf, data, false},
	}
}

// imageIdentityInputs selects the embedded files whose content actually
// determines what a base image *is* — and therefore what makes an image built on
// it stale.
//
// The runtimeBin scripts are excluded, which is the whole point (DF156). They are
// delivered from the running binary on every launch and bind-mounted over
// /yoloai/bin, on the agent container and on the firewall sidecar alike, so the
// copies baked into the image are never the ones that run. Hashing them made every
// yoloAI release that touched any of eleven frequently-edited scripts restale the
// base — and, through the chain checksum, every profile image built on it, which
// is a full rebuild of the user's apt layers because our script moved. That is the
// wrong party paying, and once delivery is unconditional there is nothing left to
// detect.
//
// The Dockerfile stays because it *is* the user-facing content: apt packages, the
// toolchains and agent CLIs their layers are built against. tmux.conf stays for a
// narrower reason worth stating, because it looks like a script and is not: its
// mount is config-gated (`tmux_conf: host` leaves /yoloai/tmux/tmux.conf unmounted
// and the baked copy live), so unlike the scripts it can still be the file that
// runs.
//
// No second "scripts changed" checksum accompanies this. Nothing would consume
// one — delivery is unconditional, so the delivered scripts are current by
// construction — and a value computed for symmetry with no reader is exactly the
// speculative API D125 forbids.
func imageIdentityInputs() []buildInput {
	var identity []buildInput
	for _, f := range baseBuildInputs() {
		if !f.runtimeBin {
			identity = append(identity, f)
		}
	}
	return identity
}

// checksumOf hashes name and content in slice order. Split out from
// buildInputsChecksum so a test can hash a set the embedded constants cannot be
// varied to produce.
func checksumOf(inputs []buildInput) string {
	h := sha256.New()
	for _, f := range inputs {
		h.Write([]byte(f.name))
		h.Write(f.content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildInputsChecksum computes the base image's identity checksum.
//
// This value changed when the runtimeBin scripts left it, so the first launch
// after upgrading past that commit restales every existing yoloai-base and
// rebuilds it once per host per backend. That is the same one-time cost the
// missing-label rule already accepts, and it buys the opposite behaviour
// afterwards: script-only releases stop rebuilding anything, and a lineage
// warning on resume once again means real package drift rather than "yoloAI
// edited a Python file".
func buildInputsChecksum() string { return checksumOf(imageIdentityInputs()) }

// WriteRuntimeScripts materialises the /yoloai/bin script set into dir, for a
// caller that bind-mounts it into the sandbox instead of relying on the copies
// baked into the image (DF156 remedy c).
//
// Delivering at launch is what decouples a yoloAI release from the user's image:
// these scripts are what the *host process* is built against, not what the user's
// Dockerfile layers are, so inheriting them through FROM couples our changing to
// their rebuilding — and a profile image predating a newly-added script cannot
// satisfy the host at all. tart has always worked this way (writeVMSetupScripts);
// this is the container backends catching up.
//
// Modes are set explicitly because the guest runs these by absolute path: the
// six the Dockerfile chmods +x must arrive executable, and there is no Dockerfile
// on this path to fix them up afterwards. WriteFilePerm rather than a plain write
// because os.WriteFile applies its mode only when *creating* — a copy left by an
// older binary would keep whatever mode it had, and an executable that lost its
// bit fails the launch rather than degrading.
//
// 0750 on the directory matches every other sandbox subdirectory. It is readable
// in the guest because the entrypoint remaps the in-container yoloai user to the
// host uid that owns it, which is also why these files need no world bits.
func WriteRuntimeScripts(dir string) error {
	if err := fileutil.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create runtime script dir %s: %w", dir, err)
	}
	for _, f := range baseBuildInputs() {
		if !f.runtimeBin {
			continue
		}
		if err := fileutil.WriteFilePerm(filepath.Join(dir, f.name), f.content, f.guestMode); err != nil {
			return fmt.Errorf("write runtime script %s: %w", f.name, err)
		}
	}
	return nil
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
func (r *Runtime) buildBaseImage(ctx context.Context, layout config.Layout, progress feedback.ProgressSink, logger *slog.Logger) error {
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
	// The child's output becomes one progress record per line, and is also teed
	// into a tail buffer so a failure's actionable cause rides on the error
	// itself rather than only on the (maybe discarded) stream — same value on
	// both so os/exec keeps its single-pipe path (DF144).
	tail := sysexec.NewTailBuffer(buildErrorTailLines)
	pw := feedback.NewProgressWriter(progress, "image.build_output")
	defer pw.Flush()
	w := io.MultiWriter(pw, tail)
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

	// Mode 0644 for every entry, deliberately not buildInput.guestMode: the
	// Dockerfile chmods what it needs after COPY, so the tar mode is inert here,
	// and varying it would change the COPY layers' content and cost a cache miss
	// on the next base build for no behavioural gain.
	for _, f := range baseBuildInputs() {
		header := &tar.Header{
			Name:    f.name,
			Size:    int64(len(f.content)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", f.name, err)
		}
		if _, err := tw.Write(f.content); err != nil {
			return nil, fmt.Errorf("write tar content for %s: %w", f.name, err)
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
func (r *Runtime) BuildProfileImage(ctx context.Context, sourceDir, tag, checksum string, secrets []string, buildEnv config.Layout, progress feedback.ProgressSink, notices feedback.Sink, logger *slog.Logger) error {
	buildCtx, err := createProfileBuildContext(sourceDir)
	if err != nil {
		return fmt.Errorf("create profile build context: %w", err)
	}

	args := append([]string{"build"}, AttestationOptOutFlags(r.binaryName)...)
	// Stamp the checksum on the image, exactly as buildBaseImage does for the
	// base. This is what lets staleness travel with the image rather than sit in
	// a file beside the profile (DF150/DF152/DF154).
	if checksum != "" {
		args = append(args, "--label", runtime.ProfileChecksumLabel+"="+checksum)
	}
	args = append(args, "-t", tag)
	for _, s := range secrets {
		args = append(args, "--secret", s)
	}
	args = append(args, "-")

	logger.Debug("building profile image via BuildKit", "tag", tag, "sourceDir", sourceDir, "secrets", len(secrets))

	cmd := sysexec.CommandContext(ctx, buildEnv.Env().EnvForDockerBuild(), r.binaryName, args...)
	cmd.Stdin = buildCtx
	tail := sysexec.NewTailBuffer(buildErrorTailLines)
	pw := feedback.NewProgressWriter(progress, "image.build_output")
	defer pw.Flush()
	w := io.MultiWriter(pw, tail)
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

// ImageLabels reads tag's labels from this daemon, implementing
// runtime.ProfileImageBuilder.
//
// ok distinguishes "the image is not here" from "the image is here and carries
// no labels" — the first means the caller cannot vouch for anything, the second
// is a real answer about a real image (one built before yoloAI stamped labels).
func (r *Runtime) ImageLabels(ctx context.Context, tag string) (map[string]string, bool) {
	insp, err := r.client.ImageInspect(ctx, tag)
	if err != nil {
		return nil, false
	}
	if insp.Config == nil {
		return map[string]string{}, true
	}
	return insp.Config.Labels, true
}

// BuildInputsChecksum is the checksum of the embedded base-image build inputs —
// the Dockerfile and every script COPYed into the image. Exported so backends
// that build the base by shelling out to docker (containerd) or to their own CLI
// (apple) can stamp the same value this package stamps, instead of each deriving
// its own notion of base identity.
func BuildInputsChecksum() string { return buildInputsChecksum() }

// ExpectedBaseChecksum implements runtime.ProfileImageBuilder. It is the same
// value baseImageStale compares the base image's label against, so a caller
// asking "is this instance's lineage current?" evaluates the identical predicate
// Setup uses to decide whether to rebuild.
func (r *Runtime) ExpectedBaseChecksum() string { return buildInputsChecksum() }

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
		if strings.HasPrefix(name, legacyMarkerPrefix) || name == "config.yaml" {
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

// WriteRuntimeScripts implements runtime.RuntimeScriptProvider. The package-level
// function is the implementation; this method exposes it as a backend capability
// so the launch path can ask "does this backend bake?" rather than name docker.
func (r *Runtime) WriteRuntimeScripts(dir string) error { return WriteRuntimeScripts(dir) }
