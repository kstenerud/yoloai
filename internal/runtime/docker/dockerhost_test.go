package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeContextStore lays out a docker config dir with an optional
// currentContext and a set of named contexts mapping to docker endpoint hosts,
// mirroring the CLI's contexts/meta/<sha256(name)>/meta.json layout.
func writeContextStore(t *testing.T, currentContext string, contexts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if currentContext != "" {
		cfg := `{"currentContext":` + `"` + currentContext + `"}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600))
	}
	for name, host := range contexts {
		sum := sha256.Sum256([]byte(name))
		metaDir := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]))
		require.NoError(t, os.MkdirAll(metaDir, 0o750))
		meta := `{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `"}}}`
		require.NoError(t, os.WriteFile(filepath.Join(metaDir, "meta.json"), []byte(meta), 0o600))
	}
	return dir
}

func TestResolveDockerHost_DockerHostWins(t *testing.T) {
	dir := writeContextStore(t, "desktop-linux", map[string]string{
		"desktop-linux": "unix:///home/u/.docker/run/docker.sock",
	})
	got := resolveDockerHost(map[string]string{
		"DOCKER_HOST":   "tcp://explicit:2375",
		"DOCKER_CONFIG": dir,
	})
	assert.Equal(t, "tcp://explicit:2375", got)
}

func TestResolveDockerHost_ActiveContextFromConfig(t *testing.T) {
	dir := writeContextStore(t, "desktop-linux", map[string]string{
		"desktop-linux": "unix:///home/u/.docker/run/docker.sock",
		"orbstack":      "unix:///home/u/.orbstack/run/docker.sock",
	})
	got := resolveDockerHost(map[string]string{"DOCKER_CONFIG": dir})
	assert.Equal(t, "unix:///home/u/.docker/run/docker.sock", got)
}

func TestResolveDockerHost_DockerContextEnvOverridesConfig(t *testing.T) {
	dir := writeContextStore(t, "desktop-linux", map[string]string{
		"desktop-linux": "unix:///home/u/.docker/run/docker.sock",
		"orbstack":      "unix:///home/u/.orbstack/run/docker.sock",
	})
	got := resolveDockerHost(map[string]string{
		"DOCKER_CONFIG":  dir,
		"DOCKER_CONTEXT": "orbstack",
	})
	assert.Equal(t, "unix:///home/u/.orbstack/run/docker.sock", got)
}

func TestResolveDockerHost_DefaultContextYieldsEmpty(t *testing.T) {
	dir := writeContextStore(t, "default", nil)
	got := resolveDockerHost(map[string]string{"DOCKER_CONFIG": dir})
	assert.Empty(t, got, "the reserved 'default' context must defer to the SDK default socket")
}

func TestResolveDockerHost_NoConfigYieldsEmpty(t *testing.T) {
	got := resolveDockerHost(map[string]string{"DOCKER_CONFIG": t.TempDir()})
	assert.Empty(t, got)
}

func TestResolveDockerHost_MalformedConfigDegradesToEmpty(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600))
	got := resolveDockerHost(map[string]string{"DOCKER_CONFIG": dir})
	assert.Empty(t, got)
}

func TestResolveDockerHost_ContextSelectedButMetaMissing(t *testing.T) {
	dir := writeContextStore(t, "ghost", nil) // currentContext set, no meta dir
	got := resolveDockerHost(map[string]string{"DOCKER_CONFIG": dir})
	assert.Empty(t, got)
}

func TestWellKnownDockerSockets_IncludesProvidersUnderHome(t *testing.T) {
	got := wellKnownDockerSockets(map[string]string{"HOME": "/home/u"})
	assert.Equal(t, []string{
		"unix:///var/run/docker.sock",
		"unix:///home/u/.orbstack/run/docker.sock", // OrbStack preferred over Docker Desktop
		"unix:///home/u/.docker/run/docker.sock",
		"unix:///home/u/.colima/default/docker.sock",
		"unix:///home/u/.rd/docker.sock",
	}, got)
}

func TestSockExists(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.sock")
	require.NoError(t, os.WriteFile(live, nil, 0o600))

	assert.True(t, sockExists("unix://"+live), "existing unix socket file")
	assert.False(t, sockExists("unix://"+filepath.Join(dir, "missing.sock")), "absent unix socket")
	assert.True(t, sockExists("tcp://host:2375"), "non-unix host can't be stat'd; assumed present")
	assert.False(t, sockExists(""), "empty host is not present")
}

func TestDisplayHost(t *testing.T) {
	assert.Equal(t, "the default socket", displayHost(""))
	assert.Equal(t, "unix:///x.sock", displayHost("unix:///x.sock"))
}
