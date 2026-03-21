// Package runtime defines the pluggable Runtime interface for sandbox backends.
package runtime

// IsolationContainerRuntime returns the OCI runtime name for the given isolation
// mode, or "" for the backend default (standard runc).
func IsolationContainerRuntime(isolation string) string {
	switch isolation {
	case "container-enhanced":
		return "runsc"
	case "vm":
		return "io.containerd.kata.v2"
	case "vm-enhanced":
		return "io.containerd.kata-fc.v2"
	default:
		return ""
	}
}

// IsolationSnapshotter returns the containerd snapshotter for the given isolation
// mode, or "" to use the backend default (overlayfs).
func IsolationSnapshotter(isolation string) string {
	if isolation == "vm-enhanced" {
		return "devmapper"
	}
	return ""
}
