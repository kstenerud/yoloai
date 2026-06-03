// ABOUTME: Tests for BackendType typed-enum constants — values match the
// ABOUTME: strings used by the registry today.

package runtime

import "testing"

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
