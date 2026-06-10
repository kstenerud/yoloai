// ABOUTME: Tests for HostEnv per-purpose env curation — verifies each accessor
// ABOUTME: carries exactly the keys its subprocess needs and drops the rest.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TMPDIR must survive into the daemon-discovery subset: macOS `podman machine
// inspect` derives the machine API socket path from $TMPDIR/podman/...; dropping
// it makes podman report the non-existent /tmp fallback and socket discovery
// fails with "no podman socket found". Regression guard for that bug.
func TestEnvForDaemonDiscovery_CarriesTMPDIR(t *testing.T) {
	layout := Layout{}.WithEnv(map[string]string{
		"TMPDIR":      "/var/folders/h8/abc/T/",
		"HOME":        "/home/tester",
		"DOCKER_HOST": "unix:///var/run/docker.sock",
		"SECRET_KEY":  "should-not-pass",
	})

	env := layout.Env().EnvForDaemonDiscovery()

	assert.Equal(t, "/var/folders/h8/abc/T/", env["TMPDIR"], "TMPDIR must reach podman machine socket discovery")
	assert.Equal(t, "/home/tester", env["HOME"])
	assert.Equal(t, "unix:///var/run/docker.sock", env["DOCKER_HOST"])
	assert.NotContains(t, env, "SECRET_KEY", "non-allowlisted vars must not leak into daemon discovery")
}
