// ABOUTME: Pins the BackendType enum's wire values. These strings are persisted
// ABOUTME: to environment.json and written in config files, so changing one
// ABOUTME: silently breaks existing sandboxes — this guards that contract.

package runtime

import "testing"

// TestBackendTypeConstants is a deliberate golden-value test, not a tautology:
// the BackendType constants are the on-disk wire format (environment.json
// "backend", config "backend:"). A value drift here would break sandbox loads
// and migrations for every install, so the literal strings are pinned.
func TestBackendTypeConstants(t *testing.T) {
	cases := []struct {
		got  BackendType
		want string
	}{
		{BackendDocker, "docker"},
		{BackendPodman, "podman"},
		{BackendTart, "tart"},
		{BackendSeatbelt, "seatbelt"},
		{BackendContainerd, "containerd"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("BackendType(%v) = %q, want %q", c.got, string(c.got), c.want)
		}
	}
}
