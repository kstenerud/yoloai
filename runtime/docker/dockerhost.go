// ABOUTME: Resolves which Docker daemon endpoint to connect to, mirroring the
// ABOUTME: docker CLI's DOCKER_HOST > active-context > default-socket priority.
package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const unixScheme = "unix://"

// resolveDockerHost picks the Docker endpoint the way the docker CLI does, but
// sourced from the threaded env snapshot rather than os.Environ (§12):
//
//  1. DOCKER_HOST, if set, wins outright.
//  2. otherwise the active context (DOCKER_CONTEXT env, else the config.json
//     currentContext) supplies its docker endpoint.
//  3. otherwise (no context, or the reserved "default" context) it returns ""
//     and the SDK falls back to its built-in default socket.
//
// Any read/parse failure degrades silently to "" so a malformed or absent
// context store never blocks a connection the default socket could still serve.
// This closes the gap where `docker context use X` retargets the CLI but the
// Go SDK (client.FromEnv) keeps hitting the default socket.
func resolveDockerHost(env map[string]string) string {
	if h := env["DOCKER_HOST"]; h != "" {
		return h
	}
	dir := dockerConfigDir(env)
	name := activeContextName(dir, env)
	if name == "" || name == "default" {
		return ""
	}
	return contextEndpointHost(dir, name)
}

// dockerConfigDir resolves the docker config directory: DOCKER_CONFIG if set,
// else <HOME>/.docker. HOME comes from the threaded env snapshot, never
// os.Environ/os.UserHomeDir (§12: only cliutil owns home resolution).
func dockerConfigDir(env map[string]string) string {
	if d := env["DOCKER_CONFIG"]; d != "" {
		return d
	}
	if home := env["HOME"]; home != "" {
		return filepath.Join(home, ".docker")
	}
	return ""
}

// activeContextName returns the selected context name: DOCKER_CONTEXT env wins,
// else the config.json currentContext field. "" means "no explicit selection".
func activeContextName(configDir string, env map[string]string) string {
	if c := env["DOCKER_CONTEXT"]; c != "" {
		return c
	}
	if configDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(configDir, "config.json")) //nolint:gosec // reads the caller's own docker config (DOCKER_CONFIG/HOME), not attacker input
	if err != nil {
		return ""
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.CurrentContext
}

// contextEndpointHost reads the docker endpoint Host for a named context from
// the CLI's on-disk context store, laid out as
// <configDir>/contexts/meta/<sha256(name)>/meta.json. Returns "" if the context
// has no docker endpoint or can't be read.
func contextEndpointHost(configDir, name string) string {
	if configDir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(name))
	metaPath := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	data, err := os.ReadFile(metaPath) //nolint:gosec // reads the caller's own docker context store, not attacker input
	if err != nil {
		return ""
	}
	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Endpoints.Docker.Host
}

// dockerProviders are the local Docker-compatible daemons yoloai knows by name,
// in preference order (OrbStack before Docker Desktop). rel is the socket path
// under $HOME. The table feeds both the dead-socket fallback prober
// (wellKnownDockerSockets) and the "you may have switched providers" hint
// (detectedDockerProviders), so the two never drift.
var dockerProviders = []struct{ rel, name string }{
	{".orbstack/run/docker.sock", "OrbStack"},
	{".docker/run/docker.sock", "Docker Desktop"},
	{".colima/default/docker.sock", "Colima"},
	{".rd/docker.sock", "Rancher Desktop"},
}

// wellKnownDockerSockets lists local Docker-compatible unix sockets to probe
// when the resolved endpoint is dead, in preference order. The caller skips
// paths that don't exist. HOME is sourced from the threaded env (§12).
func wellKnownDockerSockets(env map[string]string) []string {
	out := []string{unixScheme + "/var/run/docker.sock"}
	if home := env["HOME"]; home != "" {
		for _, p := range dockerProviders {
			out = append(out, unixScheme+filepath.Join(home, p.rel))
		}
	}
	return out
}

// detectedDockerProviders returns the product names of local Docker providers
// whose daemon socket is present on disk under homeDir, in preference order. It
// powers the switch-provider hint: when the active daemon doesn't have a
// sandbox's container (or no daemon is reachable), it points the user at the
// providers actually installed so they can start the one they created on.
func detectedDockerProviders(homeDir string) []string {
	if homeDir == "" {
		return nil
	}
	var out []string
	for _, p := range dockerProviders {
		if sockExists(unixScheme + filepath.Join(homeDir, p.rel)) {
			out = append(out, p.name)
		}
	}
	return out
}

// sockExists reports whether host names a reachable-looking endpoint without
// dialing: for unix sockets the file must exist on disk; non-unix hosts (tcp,
// ssh, npipe) can't be stat'd so a non-empty value is treated as present.
func sockExists(host string) bool {
	if strings.HasPrefix(host, unixScheme) {
		_, err := os.Stat(strings.TrimPrefix(host, unixScheme))
		return err == nil
	}
	return host != ""
}

// displayHost renders an endpoint for user-facing messages; "" is the default.
func displayHost(host string) string {
	if host == "" {
		return "the default socket"
	}
	return host
}
