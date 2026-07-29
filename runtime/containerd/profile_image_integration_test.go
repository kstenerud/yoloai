//go:build integration && linux

// ABOUTME: Real-daemon coverage for containerd's ProfileImageBuilder (DF153):
// ABOUTME: a profile Dockerfile is built by docker and lands in the yoloai
// ABOUTME: containerd namespace with a complete, GC-traceable descriptor tree.

package containerdrt

import (
	"context"
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

	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
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
	err = rt.BuildProfileImage(ctx, profileDir, tag, nil, layout, &out, slog.New(slog.DiscardHandler))
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

// TestIntegration_ProfileImageChecksum_IsKeyedToContainerd pins DF150's invariant
// on this backend: the profile directory is shared across backends but the image
// stores are not, so a build recorded by docker must not tell containerd its own
// image is fresh. containerd is the sharpest case — it builds *via* docker, so the
// two stores are unusually easy to conflate.
func TestIntegration_ProfileImageChecksum_IsKeyedToContainerd(t *testing.T) {
	requireDaemon(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	profileDir := t.TempDir()
	parentDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(profileDir, "Dockerfile"),
		[]byte("FROM yoloai-base\n"), 0600))

	require.True(t, rt.ProfileImageNeedsBuild(profileDir, parentDir), "nothing built yet")

	// A docker-side build must not satisfy containerd.
	dockerrt.RecordProfileBuildChecksum(profileDir, "docker")
	assert.True(t, rt.ProfileImageNeedsBuild(profileDir, parentDir),
		"docker built it into docker's store; containerd never received it (DF150)")

	rt.RecordProfileBuildChecksum(profileDir)
	assert.False(t, rt.ProfileImageNeedsBuild(profileDir, parentDir),
		"containerd's own record is what clears containerd")
}

var _ io.Writer = (*strings.Builder)(nil)
