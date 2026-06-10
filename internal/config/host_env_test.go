// ABOUTME: Tests for HostEnv per-purpose env curation — verifies each accessor
// ABOUTME: carries exactly the keys its subprocess needs and drops the rest.

package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// The Apple `container` CLI keyset passes HOME through unchanged (the apiserver is
// a shared per-user launchd agent that resolves its state root from the real HOME),
// carries the CONTAINER_* knobs, and drops the host SSH agent and arbitrary
// secrets. Overriding HOME the way EnvForDockerExec does would desync the CLI from
// the daemon, so this guards the passthrough.
func TestEnvForAppleContainer_PassesHomeAndDropsSSHAgent(t *testing.T) {
	layout := Layout{HomeDir: "/layout/home"}.WithEnv(map[string]string{
		"PATH":               "/usr/local/bin:/usr/bin",
		"HOME":               "/Users/real",
		"CONTAINER_APP_ROOT": "/custom/state",
		"SSH_AUTH_SOCK":      "/tmp/agent.sock",
		"SECRET_KEY":         "should-not-pass",
	})

	env := envSliceToMap(layout.Env().EnvForAppleContainer())

	assert.Equal(t, "/Users/real", env["HOME"], "HOME must pass through (not be overridden to the layout home) so the CLI and the shared daemon agree on the state root")
	assert.Equal(t, "/usr/local/bin:/usr/bin", env["PATH"])
	assert.Equal(t, "/custom/state", env["CONTAINER_APP_ROOT"])
	assert.NotContains(t, env, "SSH_AUTH_SOCK", "the host SSH agent must not be forwarded into a sandbox")
	assert.NotContains(t, env, "SECRET_KEY", "non-allowlisted vars must not leak to the container CLI")
}

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
