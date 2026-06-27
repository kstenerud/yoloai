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

// TestSupportsAgentFreeLaunch guards the contract that the launch orchestrator
// relies on: container-enhanced (gVisor) is the one isolation mode where the D88
// keepalive+Launch host-side `exec --user yoloai` resolves against gVisor's stale
// image passwd (wrong UID → can't write the remapped /yoloai dirs → agent never
// welds), so it must report false and fall back to the legacy in-entrypoint weld.
// Every other mode supports the agent-free path.
func TestSupportsAgentFreeLaunch(t *testing.T) {
	cases := map[IsolationMode]bool{
		IsolationModeDefault:             true,
		IsolationModeContainer:           true,
		IsolationModeContainerPrivileged: true,
		IsolationModeContainerEnhanced:   false, // gVisor — stale exec --user resolution
		IsolationModeVM:                  true,
		IsolationModeVMEnhanced:          true,
	}
	for isolation, want := range cases {
		if got := SupportsAgentFreeLaunch(isolation); got != want {
			t.Errorf("SupportsAgentFreeLaunch(%q) = %v, want %v", isolation, got, want)
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
		macMajor  int
		container bool
		want      bool
	}{
		// container-privileged: allowed on a darwin host targeting Linux
		// containers (the regression this guards — it used to be blocked).
		{"priv darwin host, linux target", IsolationModeContainerPrivileged, "linux", "darwin", 0, false, true},
		{"priv darwin host, default target", IsolationModeContainerPrivileged, "", "darwin", 0, false, true},
		{"priv linux host", IsolationModeContainerPrivileged, "", "linux", 0, false, true},
		// ...but not when explicitly targeting macOS-native backends.
		{"priv os=mac rejected", IsolationModeContainerPrivileged, "mac", "darwin", 0, false, false},
		// VM on macOS now routes to Apple `container` — three paths:
		{"vm darwin, container installed + macOS 26", IsolationModeVM, "linux", "darwin", 26, true, true},
		{"vm darwin, container not installed", IsolationModeVM, "linux", "darwin", 26, false, false},
		{"vm darwin, macOS too old (even with container)", IsolationModeVM, "linux", "darwin", 15, true, false},
		// vm-enhanced (gVisor-in-VM) has no macOS backend (apple is a plain VM).
		{"vm-enhanced darwin rejected", IsolationModeVMEnhanced, "linux", "darwin", 26, true, false},
		{"vm linux host ok", IsolationModeVM, "", "linux", 0, false, true},
		// container-enhanced (gVisor): rejected on a darwin host entirely (D71) —
		// the macOS Docker VMs can't run runsc turn-key (Docker Desktop engine
		// fails on registration; OrbStack /tmp chroot; cgroup hazard). gVisor is
		// Linux-primary. Both --os mac and the host-darwin/linux-target case fail.
		{"enhanced darwin host, linux target rejected", IsolationModeContainerEnhanced, "linux", "darwin", 0, false, false},
		{"enhanced darwin host, default target rejected", IsolationModeContainerEnhanced, "", "darwin", 0, false, false},
		{"enhanced os=mac rejected", IsolationModeContainerEnhanced, "mac", "darwin", 0, false, false},
		{"enhanced linux host ok", IsolationModeContainerEnhanced, "", "linux", 0, false, true},
		// Plain container is always fine.
		{"container darwin host", IsolationModeContainer, "", "darwin", 0, false, true},
		// microvm: Linux/KVM only. Available on a Linux host targeting Linux;
		// rejected on a darwin host or when targeting macOS-native backends.
		{"microvm linux host ok", IsolationModeMicroVM, "", "linux", 0, false, true},
		{"microvm darwin host rejected", IsolationModeMicroVM, "", "darwin", 0, false, false},
		{"microvm os=mac rejected", IsolationModeMicroVM, "mac", "linux", 0, false, false},
	}
	for _, c := range cases {
		got, _, _ := IsolationAvailability(c.isolation, c.targetOS, c.hostOS, c.macMajor, c.container)
		if got != c.want {
			t.Errorf("%s: IsolationAvailability(%q, %q, %q, %d, %v) = %v, want %v",
				c.name, c.isolation, c.targetOS, c.hostOS, c.macMajor, c.container, got, c.want)
		}
	}
}
