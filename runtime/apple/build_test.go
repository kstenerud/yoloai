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
// Dockerfile — the only file createProfileBuildContext requires to build a
// tar context (config.yaml and the checksum marker are filtered out, not
// required in).
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
	err := r.BuildProfileImage(context.Background(), sourceDir, "yoloai-r-dev", nil, r.layout, &output, slog.New(slog.DiscardHandler))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container build:",
		"the error names the failed operation")
	assert.Contains(t, output.String(), cause,
		"the build tool's own diagnostic still reaches the caller's output stream, even though "+
			"(unlike buildBaseImage) BuildProfileImage does not also fold it onto the error itself")
}

func TestBuildProfileImage_WarnsOnDroppedSecrets(t *testing.T) {
	r := newFakeContainerRuntime(t, "#!/bin/sh\nexit 0\n")
	sourceDir := newFakeProfileDir(t)

	var output strings.Builder
	err := r.BuildProfileImage(context.Background(), sourceDir, "yoloai-r-dev", []string{"npmrc"}, r.layout, &output, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	assert.Contains(t, output.String(), "not supported on the apple backend",
		"an auto-detected build secret must be reported, not silently dropped")
	assert.Contains(t, output.String(), "1 secret(s)")
}

func TestBuildProfileImage_NoWarningWithoutSecrets(t *testing.T) {
	r := newFakeContainerRuntime(t, "#!/bin/sh\nexit 0\n")
	sourceDir := newFakeProfileDir(t)

	var output strings.Builder
	err := r.BuildProfileImage(context.Background(), sourceDir, "yoloai-r-dev", nil, r.layout, &output, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	assert.Empty(t, output.String(), "no secrets means no warning noise")
}
