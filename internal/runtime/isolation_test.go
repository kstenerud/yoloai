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

// TestIsolationAvailability locks in the host/target-OS rules, with emphasis on
// container-privileged: a darwin *host* is fine (Docker Desktop / OrbStack /
// Podman Machine run --privileged inside their Linux VM), so the only privileged
// rejection is the macOS-native target (--os mac → seatbelt/tart).
func TestIsolationAvailability(t *testing.T) {
	cases := []struct {
		name      string
		isolation IsolationMode
		targetOS  string
		hostOS    string
		want      bool
	}{
		// container-privileged: allowed on a darwin host targeting Linux
		// containers (the regression this guards — it used to be blocked).
		{"priv darwin host, linux target", IsolationModeContainerPrivileged, "linux", "darwin", true},
		{"priv darwin host, default target", IsolationModeContainerPrivileged, "", "darwin", true},
		{"priv linux host", IsolationModeContainerPrivileged, "", "linux", true},
		// ...but not when explicitly targeting macOS-native backends.
		{"priv os=mac rejected", IsolationModeContainerPrivileged, "mac", "darwin", false},
		// VM modes still require containerd, unavailable on a darwin host.
		{"vm darwin host rejected", IsolationModeVM, "linux", "darwin", false},
		{"vm linux host ok", IsolationModeVM, "", "linux", true},
		// container-enhanced (gVisor): rejected on a darwin host entirely (D71) —
		// the macOS Docker VMs can't run runsc turn-key (Docker Desktop engine
		// fails on registration; OrbStack /tmp chroot; cgroup hazard). gVisor is
		// Linux-primary. Both --os mac and the host-darwin/linux-target case fail.
		{"enhanced darwin host, linux target rejected", IsolationModeContainerEnhanced, "linux", "darwin", false},
		{"enhanced darwin host, default target rejected", IsolationModeContainerEnhanced, "", "darwin", false},
		{"enhanced os=mac rejected", IsolationModeContainerEnhanced, "mac", "darwin", false},
		{"enhanced linux host ok", IsolationModeContainerEnhanced, "", "linux", true},
		// Plain container is always fine.
		{"container darwin host", IsolationModeContainer, "", "darwin", true},
	}
	for _, c := range cases {
		got, _, _ := IsolationAvailability(c.isolation, c.targetOS, c.hostOS)
		if got != c.want {
			t.Errorf("%s: IsolationAvailability(%q, %q, %q) = %v, want %v",
				c.name, c.isolation, c.targetOS, c.hostOS, got, c.want)
		}
	}
}
