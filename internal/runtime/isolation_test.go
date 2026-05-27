// ABOUTME: Tests for isolation-mode helpers (OCI runtime mapping, snapshotter, network enforcement).
package runtime

import "testing"

func TestIsolationContainerRuntime(t *testing.T) {
	cases := map[IsolationMode]string{
		IsolationModeDefault:             "",
		IsolationModeContainer:           "",
		IsolationModeContainerPrivileged: "",
		IsolationModeContainerEnhanced:   "runsc",
		IsolationModeVM:                  "io.containerd.kata.v2",
		IsolationModeVMEnhanced:          "io.containerd.kata-fc.v2",
		"unknown":                        "",
	}
	for in, want := range cases {
		if got := IsolationContainerRuntime(in); got != want {
			t.Errorf("IsolationContainerRuntime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsolationSnapshotter(t *testing.T) {
	cases := map[IsolationMode]string{
		IsolationModeDefault:             "",
		IsolationModeContainer:           "",
		IsolationModeContainerPrivileged: "",
		IsolationModeContainerEnhanced:   "",
		IsolationModeVM:                  "",
		IsolationModeVMEnhanced:          "devmapper",
	}
	for in, want := range cases {
		if got := IsolationSnapshotter(in); got != want {
			t.Errorf("IsolationSnapshotter(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIsolationEnforcesInSandboxIptables guards the contract that
// create_instance.go relies on: container-enhanced (gVisor) is the one
// isolation mode where in-sandbox iptables doesn't fire, so it must report
// false. Everything else must report true so --network-isolated isn't
// rejected on paths that work.
func TestIsolationEnforcesInSandboxIptables(t *testing.T) {
	cases := map[IsolationMode]bool{
		IsolationModeDefault:             true, // backend default — standard runc, enforces
		IsolationModeContainer:           true,
		IsolationModeContainerPrivileged: true,
		IsolationModeContainerEnhanced:   false, // gVisor — does NOT enforce
		IsolationModeVM:                  true,  // Kata guest kernel enforces
		IsolationModeVMEnhanced:          true,  // Kata + Firecracker — same story
	}
	for isolation, want := range cases {
		if got := IsolationEnforcesInSandboxIptables(isolation); got != want {
			t.Errorf("IsolationEnforcesInSandboxIptables(%q) = %v, want %v", isolation, got, want)
		}
	}
}
