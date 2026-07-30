// ABOUTME: Tests that the base image's Dockerfile is materialised on disk as a
// ABOUTME: readable reference, and refreshed rather than left to go stale.

package orchestrator

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

func scaffoldInto(t *testing.T, dir string) config.Layout {
	t.Helper()
	layout := config.Layout{DataDir: dir}
	e := NewEngineWithRuntime(nil, slog.New(slog.DiscardHandler), nil, WithLayout(layout))
	require.NoError(t, e.ensureLayoutScaffold())
	return layout
}

// TestScaffold_MaterialisesTheBaseDockerfile covers the reason it exists: the
// image is built from a Dockerfile compiled into the binary, so without this
// there is no way for a user to read what the sandbox they were handed contains.
func TestScaffold_MaterialisesTheBaseDockerfile(t *testing.T) {
	layout := scaffoldInto(t, t.TempDir())

	path := filepath.Join(layout.DefaultsDir(), "base-image.Dockerfile")
	written, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err, "the base Dockerfile must be readable on disk, or the image is undocumented")
	assert.Equal(t, dockerrt.BaseDockerfile(), written, "it must be this binary's copy, byte for byte")

	assert.Contains(t, string(written), "EDITING THIS FILE HAS NO EFFECT",
		"the copy must say it is a copy: it sits beside profile Dockerfiles that ARE read from disk, "+
			"so a reader's default assumption is the wrong one")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// TestScaffold_RefreshesAStaleBaseDockerfile is the half that distinguishes this
// from the tmux.conf beside it. That one is written only when absent, because a
// user may customise it and the sandbox mount then binds their copy. This one has
// no such role, so "leave what is there" would let it describe an image two
// releases old — and a reference copy that can go stale is worse than none,
// because it reads as authoritative and answers the question wrong.
func TestScaffold_RefreshesAStaleBaseDockerfile(t *testing.T) {
	dir := t.TempDir()
	layout := scaffoldInto(t, dir)
	path := filepath.Join(layout.DefaultsDir(), "base-image.Dockerfile")

	require.NoError(t, os.WriteFile(path, []byte("FROM debian:ancient\n"), 0o644)) //nolint:gosec // G306: mirrors production mode

	scaffoldInto(t, dir)

	written, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, dockerrt.BaseDockerfile(), written,
		"a stale copy must be overwritten, not preserved")
	assert.False(t, strings.Contains(string(written), "debian:ancient"))
}

// TestScaffold_LeavesACustomisedTmuxConfAlone guards the other side of that
// asymmetry, so a later change cannot make both files behave the same way by
// tidying: tmux.conf IS user-customisable and is bind-mounted from here.
func TestScaffold_LeavesACustomisedTmuxConfAlone(t *testing.T) {
	dir := t.TempDir()
	layout := scaffoldInto(t, dir)
	path := filepath.Join(layout.DefaultsDir(), "tmux.conf")

	const custom = "set -g status off\n# my own config\n"
	require.NoError(t, os.WriteFile(path, []byte(custom), 0o644)) //nolint:gosec // G306: mirrors production mode

	scaffoldInto(t, dir)

	written, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, custom, string(written), "a user's tmux.conf must survive: the sandbox mounts this file")
}
