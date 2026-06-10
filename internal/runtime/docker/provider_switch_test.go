// ABOUTME: Tests for the "you may have switched Docker providers" diagnostics —
// ABOUTME: provider detection, the not-found hint, and the ping-failure hint.

package docker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFakeSocket(t *testing.T, home, rel string) {
	t.Helper()
	p := filepath.Join(home, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, nil, 0o600))
}

func TestDetectedDockerProviders(t *testing.T) {
	home := t.TempDir()
	// OrbStack + Docker Desktop present; Colima / Rancher absent.
	writeFakeSocket(t, home, ".orbstack/run/docker.sock")
	writeFakeSocket(t, home, ".docker/run/docker.sock")

	assert.Equal(t, []string{"OrbStack", "Docker Desktop"}, detectedDockerProviders(home),
		"only installed providers are reported, in preference order")
}

func TestDetectedDockerProviders_EmptyCases(t *testing.T) {
	assert.Nil(t, detectedDockerProviders(""), "no home → no providers")
	assert.Nil(t, detectedDockerProviders(t.TempDir()), "empty home → no providers")
}

func TestNotFound_HintOnlyWhenMultipleProviders(t *testing.T) {
	multi := &Runtime{providerNames: []string{"OrbStack", "Docker Desktop"}}
	err := multi.notFound()
	assert.ErrorIs(t, err, runtime.ErrNotFound, "the hint must still wrap ErrNotFound for errors.Is callers")
	assert.Contains(t, err.Error(), "OrbStack")
	assert.Contains(t, err.Error(), "switched Docker providers")

	single := &Runtime{providerNames: []string{"OrbStack"}}
	assert.Equal(t, runtime.ErrNotFound, single.notFound(),
		"a single provider can't be a switch — return the bare sentinel, no noise")

	none := &Runtime{}
	assert.Equal(t, runtime.ErrNotFound, none.notFound())
}

func TestPingFailureError_NamesDetectedProviders(t *testing.T) {
	home := t.TempDir()
	writeFakeSocket(t, home, ".orbstack/run/docker.sock")

	err := pingFailureError(errors.New("dial fail"), "docker", map[string]string{"HOME": home})
	assert.Contains(t, err.Error(), "OrbStack", "the hint names the installed provider to start")
	assert.Contains(t, err.Error(), "dial fail", "the underlying error is preserved, not discarded")
}

func TestPingFailureError_FallbackHintWhenNoProvidersDetected(t *testing.T) {
	err := pingFailureError(errors.New("dial fail"), "docker", map[string]string{"HOME": t.TempDir()})
	assert.Contains(t, err.Error(), "Docker Desktop", "with nothing detected, fall back to the generic hint")
}
