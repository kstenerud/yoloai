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
