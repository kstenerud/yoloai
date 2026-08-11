// ABOUTME: Unit test for buildBaseImage's DF145 error forwarding: a failed
// ABOUTME: `container build` must carry the tail of its own output on the
// ABOUTME: returned error (the DF144 remedy, mirrored from the docker backend).

package apple

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

func TestBuildBaseImage_ErrorCarriesOutputTail(t *testing.T) {
	const cause = "ERROR: failed to resolve source metadata for docker.io/library/ubuntu"
	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + cause + "' >&2\nexit 1\n"
	fakeBin := filepath.Join(dir, "container")
	require.NoError(t, os.WriteFile(fakeBin, []byte(script), 0700)) //nolint:gosec // test fixture needs exec bit

	r := &Runtime{
		containerBin: fakeBin,
		layout:       config.NewLayout(filepath.Join(t.TempDir(), ".yoloai")).WithPrincipal(config.CLIPrincipal),
		execEnv:      []string{"PATH=/usr/bin:/bin"},
	}

	err := r.buildBaseImage(context.Background(), r.layout, io.Discard, slog.New(slog.DiscardHandler))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container build exited with code 1",
		"the error names the operation and exit code")
	assert.Contains(t, err.Error(), cause,
		"the build tool's own diagnostic rides on the error, not only the stream (DF144/DF145)")
}

func TestCreate_PassesOrderedDNSBeforeImageAndRejectsNoneBeforeDelete(t *testing.T) {
	deletedPath := filepath.Join(t.TempDir(), "deleted")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = delete ]; then touch " + deletedPath + "; exit 0; fi\n" +
		"[ \"$#\" -eq 8 ] && [ \"$1\" = create ] && [ \"$2\" = --name ] && [ \"$3\" = box ] && [ \"$4\" = --dns ] && [ \"$5\" = 1.1.1.1 ] && [ \"$6\" = --dns ] && [ \"$7\" = 8.8.8.8 ] && [ \"$8\" = image ]\n"
	r := newFakeContainerRuntime(t, script)
	err := r.Create(context.Background(), runtime.InstanceConfig{Name: "box", ImageRef: "image", DNS: []string{"1.1.1.1", "8.8.8.8"}})
	require.NoError(t, err)

	systemScript := "#!/bin/sh\n" +
		"if [ \"$1\" = delete ]; then exit 0; fi\n" +
		"case \"$*\" in *--dns*) exit 1;; esac\n"
	r = newFakeContainerRuntime(t, systemScript)
	err = r.Create(context.Background(), runtime.InstanceConfig{Name: "box", ImageRef: "image"})
	require.NoError(t, err)

	require.NoError(t, os.Remove(deletedPath))
	r = newFakeContainerRuntime(t, script)
	err = r.Create(context.Background(), runtime.InstanceConfig{Name: "box", ImageRef: "image", NetworkMode: "none"})
	assert.ErrorContains(t, err, "support --network-none")
	assert.NoFileExists(t, deletedPath, "Apple Container no-network must fail before stale-instance deletion")

	err = r.Create(context.Background(), runtime.InstanceConfig{Name: "box", ImageRef: "image", DNS: []string{"::1"}})
	assert.ErrorContains(t, err, "must be an IPv4 address")
	assert.NoFileExists(t, deletedPath, "invalid DNS must fail before stale-instance deletion")
}

// newFakeContainerRuntime builds a Runtime whose containerBin runs script
// (a full shell script body, shebang included) instead of the real `container`
// CLI, mirroring TestBuildBaseImage_ErrorCarriesOutputTail's fixture pattern.
func newFakeContainerRuntime(t *testing.T, script string) *Runtime {
	t.Helper()
	dir := t.TempDir()
	fakeBin := filepath.Join(dir, "container")
	require.NoError(t, os.WriteFile(fakeBin, []byte(script), 0700)) //nolint:gosec // test fixture needs exec bit

	return &Runtime{
		containerBin: fakeBin,
		layout:       config.NewLayout(filepath.Join(t.TempDir(), ".yoloai")).WithPrincipal(config.CLIPrincipal),
		execEnv:      []string{"PATH=/usr/bin:/bin"},
	}
}

// newFakeProfileDir writes a minimal profile directory containing just a
// Dockerfile — the only file BuildProfileImage needs in order to materialize a
// build context, which on this path is a *directory* written by
// dockerrt.WriteProfileBuildContextDir rather than the tar the docker backend
// streams. config.yaml and the per-backend checksum markers are filtered out of
// that context, so they are not required in.
func newFakeProfileDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base\n"), 0600))
	return dir
}

func TestBuildProfileImage_ErrorWrapsExitStatus(t *testing.T) {
	const cause = "ERROR: failed to solve: process did not complete successfully"
	script := "#!/bin/sh\necho '" + cause + "' >&2\nexit 1\n"
	r := newFakeContainerRuntime(t, script)
	sourceDir := newFakeProfileDir(t)

	var output strings.Builder
	err := r.BuildProfileImage(context.Background(), sourceDir, "yoloai-cli-dev", "", nil, r.layout, &output, slog.New(slog.DiscardHandler))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container build exited with code 1",
		"the error names the operation and exit code")
	assert.Contains(t, err.Error(), cause,
		"the build tool's own diagnostic rides on the error, not only the stream (DF144/DF145)")
}

// TestBuildProfileImage_PassesTagAndAbsoluteContext pins the argv this fix
// depends on (D128): `-t <tag>` must be passed, and the build context must be
// an absolute directory containing the profile's Dockerfile. A relative
// context (AC1) silently transfers nothing and every COPY fails — a defect
// no assertion on the wrapped error alone would catch.
func TestBuildProfileImage_PassesTagAndAbsoluteContext(t *testing.T) {
	const tag = "yoloai-cli-dev"
	script := "#!/bin/sh\n" +
		"[ \"$1\" = build ] || { echo \"bad subcommand: $1\" >&2; exit 2; }\n" +
		"[ \"$2\" = -t ] || { echo \"bad flag: $2\" >&2; exit 3; }\n" +
		"[ \"$3\" = " + tag + " ] || { echo \"bad tag: $3\" >&2; exit 4; }\n" +
		"case \"$4\" in\n" +
		"  /*) ;;\n" +
		"  *) echo \"context not absolute: $4\" >&2; exit 5 ;;\n" +
		"esac\n" +
		"test -f \"$4/Dockerfile\" || { echo \"Dockerfile missing from context\" >&2; exit 6; }\n" +
		"exit 0\n"
	r := newFakeContainerRuntime(t, script)
	sourceDir := newFakeProfileDir(t)

	var output strings.Builder
	err := r.BuildProfileImage(context.Background(), sourceDir, tag, "", nil, r.layout, &output, slog.New(slog.DiscardHandler))
	require.NoError(t, err, output.String())
}

// TestBuildProfileImage_DrawsEnvFromBuildEnvNotTheRuntime pins the other half
// of the ProfileImageBuilder contract (rule 10): buildEnv is the environment
// the build subprocess draws from, not the execEnv captured when the Runtime
// was constructed. The two are the same in the single-principal CLI, which is
// why using the wrong one is invisible there — it separates only for an
// embedder that builds profiles for more than one principal, and that is
// exactly the caller the contract exists for.
//
// The fake binary fails loudly if it sees the Runtime's env, so this test
// fails when the fix is reverted rather than merely covering the line.
func TestBuildProfileImage_DrawsEnvFromBuildEnvNotTheRuntime(t *testing.T) {
	script := "#!/bin/sh\n" +
		"[ -z \"$YOLOAI_RUNTIME_ENV_LEAKED\" ] || { echo 'build ran under r.execEnv, not buildEnv' >&2; exit 7; }\n" +
		"exit 0\n"
	r := newFakeContainerRuntime(t, script)
	r.execEnv = []string{"YOLOAI_RUNTIME_ENV_LEAKED=1"}
	sourceDir := newFakeProfileDir(t)

	var output strings.Builder
	err := r.BuildProfileImage(context.Background(), sourceDir, "yoloai-cli-dev", "", nil, r.layout, &output, slog.New(slog.DiscardHandler))
	require.NoError(t, err, output.String())
}

func TestBuildProfileImage_WarnsOnDroppedSecrets(t *testing.T) {
	r := newFakeContainerRuntime(t, "#!/bin/sh\nexit 0\n")
	sourceDir := newFakeProfileDir(t)

	var output strings.Builder
	err := r.BuildProfileImage(context.Background(), sourceDir, "yoloai-cli-dev", "", []string{"npmrc"}, r.layout, &output, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	assert.Contains(t, output.String(), "not supported on the apple backend",
		"an auto-detected build secret must be reported, not silently dropped")
	assert.Contains(t, output.String(), "1 secret(s)")
}

func TestBuildProfileImage_NoWarningWithoutSecrets(t *testing.T) {
	r := newFakeContainerRuntime(t, "#!/bin/sh\nexit 0\n")
	sourceDir := newFakeProfileDir(t)

	var output strings.Builder
	err := r.BuildProfileImage(context.Background(), sourceDir, "yoloai-cli-dev", "", nil, r.layout, &output, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	assert.Empty(t, output.String(), "no secrets means no warning noise")
}
