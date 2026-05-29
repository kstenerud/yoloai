// ABOUTME: Isolation-mode-derived filesystem permission values for sandbox directories
// ABOUTME: and files; used when creating host-side paths that containers will access.
package state

import (
	"os"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// IsolationPerms holds filesystem permission values that vary by isolation mode.
// Under container-enhanced (gVisor), the entrypoint remaps the container's yoloai
// user UID to the host user's UID at runtime, but files created before the remap
// (e.g. by the Go host process) are owned by the original host UID. Both UIDs need
// access, so permissions must be world-accessible.
type IsolationPerms struct {
	Dir         os.FileMode // container-owned directories (work, cache, logs, agent-state)
	File        os.FileMode // container-owned files (logs, status)
	SecretsDir  os.FileMode // ephemeral secrets dir (removed after container mount)
	SecretsFile os.FileMode // individual secret files (removed after container mount)
}

// Perms returns the filesystem permissions appropriate for the given isolation
// mode. Use this whenever creating host-side files or directories that the
// container process will write to.
func Perms(isolation runtime.IsolationMode) IsolationPerms {
	if isolation == runtime.IsolationModeContainerEnhanced {
		return IsolationPerms{
			Dir:         0777, //nolint:gosec // G301: world-writable needed for gVisor user-namespace UID remapping
			File:        0666, //nolint:gosec // G306: world-writable needed for gVisor user-namespace UID remapping
			SecretsDir:  0755, //nolint:gosec // G302: world-executable for gVisor UID remapping; removed within seconds
			SecretsFile: 0644, //nolint:gosec // G306: world-readable for gVisor UID remapping; removed within seconds
		}
	}
	return IsolationPerms{
		Dir:         0750,
		File:        0600,
		SecretsDir:  0700,
		SecretsFile: 0600,
	}
}
