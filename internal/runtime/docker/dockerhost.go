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
// else <HOME>/.docker. HOME is read from the threaded env first (§12), falling
// back to os.UserHomeDir only when the snapshot lacks it.
func dockerConfigDir(env map[string]string) string {
	if d := env["DOCKER_CONFIG"]; d != "" {
		return d
	}
	if home := homeDir(env); home != "" {
		return filepath.Join(home, ".docker")
	}
	return ""
}

func homeDir(env map[string]string) string {
	if home := env["HOME"]; home != "" {
		return home
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
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
	data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
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
	data, err := os.ReadFile(metaPath)
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

// wellKnownDockerSockets lists local Docker-compatible unix sockets to probe
// when the resolved endpoint is dead, in preference order. The caller skips
// paths that don't exist. HOME is sourced from the threaded env (§12).
func wellKnownDockerSockets(env map[string]string) []string {
	out := []string{unixScheme + "/var/run/docker.sock"}
	if home := homeDir(env); home != "" {
		out = append(out,
			unixScheme+filepath.Join(home, ".docker/run/docker.sock"),     // Docker Desktop
			unixScheme+filepath.Join(home, ".orbstack/run/docker.sock"),   // OrbStack
			unixScheme+filepath.Join(home, ".colima/default/docker.sock"), // Colima
			unixScheme+filepath.Join(home, ".rd/docker.sock"),             // Rancher Desktop
		)
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
