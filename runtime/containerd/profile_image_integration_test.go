//go:build integration && linux

// ABOUTME: Real-daemon coverage for containerd's ProfileImageBuilder (DF153):
// ABOUTME: a profile Dockerfile is built by docker and lands in the yoloai
// ABOUTME: containerd namespace with a complete, GC-traceable descriptor tree.

package containerdrt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/content"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// testProfileTag returns a collision-proof name in the test namespace, never a
// production one (testing-principles §"Namespace + time-unique names"). The
// unixnano+rand suffix keeps concurrent runs and crashed-run leftovers apart.
func testProfileTag(purpose string) string {
	return fmt.Sprintf("yoloai-test-%s-%d-%04d", purpose, time.Now().UnixNano(), rand.Intn(10000)) //nolint:gosec // G404: test resource naming, not security
}

// requireDockerBase skips when docker has no yoloai-base to build FROM. The
// profile build resolves its base against docker's store, so without it the test
// would be measuring the absence of a fixture rather than the code.
func requireDockerBase(t *testing.T) {
	t.Helper()
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not installed; containerd profile builds require it by design")
	}
	// sysexec, not exec.Command: subprocess envs are explicit here too (DEV §12).
	out, err := sysexec.CommandContext(context.Background(), []string{"PATH=" + os.Getenv("PATH")},
		dockerBin, "images", "-q", "yoloai-base").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skip("yoloai-base is not in docker's store; run `yoloai system build` first")
	}
}

// TestIntegration_BuildProfileImage_LandsInContainerdNamespace is DF153's proof.
// Before this backend implemented ProfileImageBuilder, a profile Dockerfile was
// silently ignored on containerd and every sandbox ran an unmodified base.
//
// It also exercises the parameterisation that made the implementation possible:
// containerd's image pipeline was written for the single const "yoloai-base", and
// this drives the same fast/slow import paths with an arbitrary tag. A pass means
// the descriptor tree is fully present in the yoloai namespace — the property the
// base path verifies before declaring an image ready.
func TestIntegration_BuildProfileImage_LandsInContainerdNamespace(t *testing.T) {
	requireDaemon(t)
	requireDockerBase(t)

	ctx := context.Background()
	layout := testLayout(t)
	rt, err := New(ctx, layout)
	require.NoError(t, err)
	// Registered before the image cleanup below so LIFO closes the client LAST —
	// a `defer rt.Close()` here would run before any t.Cleanup, leaving the
	// cleanup to call a closed client and silently skip the delete (observed:
	// two orphaned images in the yoloai namespace on the first run).
	t.Cleanup(func() { _ = rt.Close() })

	// A profile whose only job is to be distinguishable from the base image.
	profileDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(profileDir, "Dockerfile"),
		[]byte("FROM yoloai-base\nRUN touch /yoloai-test-profile-marker\n"), 0600))

	tag := testProfileTag("profileimg")
	t.Cleanup(func() {
		// Scoped cleanup only: this test's own resources, by exact name, in both
		// stores. Never a production-wide sweep against a shared real daemon.
		if dockerBin, lookErr := exec.LookPath("docker"); lookErr == nil {
			_ = sysexec.CommandContext(context.Background(), []string{"PATH=" + os.Getenv("PATH")},
				dockerBin, "rmi", "-f", tag).Run()
		}
		// BOTH names: the slow import path registers the fully-qualified ref that
		// `ctr import` stores under AND a short alias, so deleting only the short
		// one leaks a record into a shared real daemon (observed).
		nsCtx := rt.withNamespace(context.Background())
		_ = rt.client.ImageService().Delete(nsCtx, tag)
		_ = rt.client.ImageService().Delete(nsCtx, dockerRefFor(tag))
	})

	var out strings.Builder
	err = rt.BuildProfileImage(ctx, profileDir, tag, "", nil, layout, &out, slog.New(slog.DiscardHandler))
	// Logged unconditionally: which import path ran (zero-copy namespace link vs
	// `docker save | ctr import`) is invisible from the result and is the thing a
	// maintainer wants when this gets slow.
	t.Logf("build output:\n%s", out.String())
	require.NoError(t, err)

	// The image must be resolvable in containerd's yoloai namespace, not merely
	// in docker's store — that gap is exactly the DF153/DF154 failure shape.
	nsCtx := rt.withNamespace(ctx)
	img, err := rt.client.GetImage(nsCtx, tag)
	require.NoError(t, err, "profile image must exist in the yoloai containerd namespace")

	// And the FULL tree must be present. A root manifest alone is what a
	// half-finished link leaves behind, and it fails later at run rather than here.
	assert.NoError(t, rt.verifyDescriptorTree(nsCtx, rt.client.ContentStore(), img.Target()),
		"every blob in the descriptor tree must be accessible, or GC has a hole to fall through")
}

var _ io.Writer = (*strings.Builder)(nil)

// TestIntegration_DockerBuildLabel_SurvivesImportIntoContainerd closes DF152's
// last reasoned-not-run assumption: that a `docker build --label` "rides along
// for free" into containerd's store, so the harmonised label scheme can cover
// this backend without a second mechanism.
//
// It verifies the transport, not the feature — BuildProfileImage does not stamp
// a checksum label yet. What has to be true first is that a label set at docker
// build time is still readable after our import, whichever path that import
// takes (zero-copy namespace link, or `docker save | ctr import` on fallback).
//
// The readback deliberately walks to the OCI *image config* blob rather than
// reading `images.Image.Labels`. Those are different things: the record label is
// local metadata in containerd's bolt store, while a build label lives in the
// image config and travels with the image. Only the latter is what "staleness
// travels with the image" means, so only the latter answers the question.
func TestIntegration_DockerBuildLabel_SurvivesImportIntoContainerd(t *testing.T) {
	requireDaemon(t)
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not installed; containerd images are built by it by design")
	}

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	buildDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(buildDir, "Dockerfile"),
		[]byte("FROM alpine\nRUN true\n"), 0600))

	const labelKey, labelVal = "yoloai.test.checksum", "df152probe"
	tag := testProfileTag("ctrlabel")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	t.Cleanup(func() {
		_ = sysexec.CommandContext(context.Background(), env, dockerBin, "rmi", "-f", tag).Run()
		nsCtx := rt.withNamespace(context.Background())
		_ = rt.client.ImageService().Delete(nsCtx, tag)
		_ = rt.client.ImageService().Delete(nsCtx, dockerRefFor(tag))
	})

	build := sysexec.CommandContext(ctx, env, dockerBin, "build",
		"--provenance=false", "--sbom=false",
		"--label", labelKey+"="+labelVal, "-t", tag, buildDir)
	out, err := build.CombinedOutput()
	require.NoError(t, err, "docker build:\n%s", out)

	var importOut strings.Builder
	require.NoError(t, rt.importFromDocker(ctx, dockerBin, tag, &importOut),
		"import output:\n%s", importOut.String())
	t.Logf("import path taken:\n%s", importOut.String())

	// Walk image record -> manifest -> config blob, the way a real reader would.
	nsCtx := rt.withNamespace(ctx)
	img, err := rt.client.GetImage(nsCtx, tag)
	require.NoError(t, err, "image must be in the yoloai namespace")

	cs := rt.client.ContentStore()
	manifestBlob, err := content.ReadBlob(nsCtx, cs, img.Target())
	require.NoError(t, err)
	var manifest ocispec.Manifest
	require.NoError(t, json.Unmarshal(manifestBlob, &manifest))

	configBlob, err := content.ReadBlob(nsCtx, cs, manifest.Config)
	require.NoError(t, err, "the image config blob must be present in this namespace")
	var cfg ocispec.Image
	require.NoError(t, json.Unmarshal(configBlob, &cfg))

	assert.Equal(t, labelVal, cfg.Config.Labels[labelKey],
		"a docker-set build label must survive into containerd's store, or the harmonised "+
			"label scheme needs a second mechanism here (DF152)")
}
