// ABOUTME: Container-system aliases — user-facing ids (orbstack, docker-desktop)
// ABOUTME: that are not backends of their own but the docker backend pinned to a
// ABOUTME: specific local daemon socket. Resolved before backend selection runs.

package runtime

import "path/filepath"

// unixSocketScheme prefixes a filesystem path to form a DOCKER_HOST unix URL.
// Duplicated from docker.unixScheme (the runtime package cannot import docker —
// docker imports runtime).
const unixSocketScheme = "unix://"

// Container-system alias ids. These are NOT registered backends: each names the
// docker backend pinned to one particular local daemon socket, so an explicit
// pick targets that exact daemon instead of whatever socket auto-discovery finds
// first. ResolveContainerSystem maps them to (BackendDocker, socket); any other
// id passes through unchanged.
const (
	ContainerSystemOrbstack      BackendType = "orbstack"
	ContainerSystemDockerDesktop BackendType = "docker-desktop"
)

// containerSystemSockets maps each alias to its daemon socket path, relative to
// the user's home directory. These are the same well-known paths the docker
// fallback prober uses (docker.wellKnownDockerSockets); kept in sync by hand
// because of the import direction.
var containerSystemSockets = map[BackendType]string{
	ContainerSystemOrbstack:      ".orbstack/run/docker.sock",
	ContainerSystemDockerDesktop: ".docker/run/docker.sock",
}

// containerSystemLabels gives each alias a human-facing product name for
// listings and error hints.
var containerSystemLabels = map[BackendType]string{
	ContainerSystemOrbstack:      "OrbStack",
	ContainerSystemDockerDesktop: "Docker Desktop",
}

// ResolveContainerSystem translates a user-facing container-system id into the
// concrete (backend, DOCKER_HOST) pair to use. The docker-VM aliases (orbstack,
// docker-desktop) resolve to the docker backend with a pinned unix socket, so an
// explicit pick reaches that exact daemon rather than whatever auto-discovery
// settles on. Every other id (docker, podman, apple, tart, …) passes through
// with an empty dockerHost (auto-endpoint, or simply not docker). An alias with
// no homeDir still resolves to docker but cannot pin (empty dockerHost), falling
// back to auto-discovery.
func ResolveContainerSystem(id BackendType, homeDir string) (backend BackendType, dockerHost string) {
	rel, ok := containerSystemSockets[id]
	if !ok {
		return id, ""
	}
	if homeDir == "" {
		return BackendDocker, ""
	}
	return BackendDocker, unixSocketScheme + filepath.Join(homeDir, rel)
}

// IsContainerSystemAlias reports whether id is a docker-VM alias (orbstack,
// docker-desktop) rather than a registered backend.
func IsContainerSystemAlias(id BackendType) bool {
	_, ok := containerSystemSockets[id]
	return ok
}

// ContainerSystems returns the docker-VM alias ids in display order (OrbStack
// before Docker Desktop, matching the docker socket-discovery preference).
func ContainerSystems() []BackendType {
	return []BackendType{ContainerSystemOrbstack, ContainerSystemDockerDesktop}
}

// ContainerSystemLabel returns the human-facing product name for an alias id, or
// the raw id when it is not an alias.
func ContainerSystemLabel(id BackendType) string {
	if label, ok := containerSystemLabels[id]; ok {
		return label
	}
	return string(id)
}

// ContainerSystemSocket returns the pinned unix-socket DOCKER_HOST for an alias
// id under homeDir, or "" when id is not an alias or homeDir is empty.
func ContainerSystemSocket(id BackendType, homeDir string) string {
	_, host := ResolveContainerSystem(id, homeDir)
	return host
}
