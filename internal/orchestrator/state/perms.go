// ABOUTME: Filesystem permission values for sandbox host-side directories and files
// ABOUTME: that the container process accesses; uniform (owner-only) across isolation modes.
package state

import "os"

// IsolationPerms holds filesystem permission values for sandbox host-side paths
// that the container process reads or writes. Permissions are restrictive
// (owner-only): the container runs as the invoking host UID (store.ContainerUser
// returns that UID for every isolation mode, gVisor/container-enhanced included),
// so owner-only perms grant the sandbox access while denying every other local
// user. gVisor (runsc) enforces guest-side uid/mode faithfully against the
// host-mapped owner, so the former world-readable special-case was both
// unnecessary and a multi-tenant secrets leak — see DF20.
type IsolationPerms struct {
	Dir         os.FileMode // container-owned directories (work, cache, logs, agent-state)
	File        os.FileMode // container-owned files (logs, status)
	SecretsDir  os.FileMode // ephemeral secrets dir (removed after container mount)
	SecretsFile os.FileMode // individual secret files (removed after container mount)
}

// Perms returns the filesystem permissions for sandbox host-side paths. Use this
// whenever creating host-side files or directories the container process touches.
func Perms() IsolationPerms {
	return IsolationPerms{
		Dir:         0750,
		File:        0600,
		SecretsDir:  0700,
		SecretsFile: 0600,
	}
}
