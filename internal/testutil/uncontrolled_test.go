// ABOUTME: Tests the mandatory-infra carve-out decision logic (D112): the env
// ABOUTME: parse and BackendAbsent's fail-unless-carved-out exit code.
package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUncontrolledBackends_Parse(t *testing.T) {
	cases := map[string]struct {
		env  string
		want []string
	}{
		"unset":               {"", nil},
		"single":              {"containerd", []string{"containerd"}},
		"csv":                 {"containerd,apple", []string{"containerd", "apple"}},
		"whitespace-and-gaps": {" containerd , , apple ,", []string{"containerd", "apple"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(uncontrolledBackendsEnv, tc.env)
			got := UncontrolledBackends()
			assert.Len(t, got, len(tc.want))
			for _, w := range tc.want {
				assert.True(t, got[w], "expected %q carved out", w)
			}
		})
	}
}

// TestBackendAbsent_ExitCode is the policy in one assertion: a platform-possible
// backend that's absent FAILS (exit 1); only a carved-out backend downgrades to a
// clean skip (exit 0). Absence of one backend does not carve out a different one.
func TestBackendAbsent_ExitCode(t *testing.T) {
	t.Run("not carved -> fail", func(t *testing.T) {
		t.Setenv(uncontrolledBackendsEnv, "")
		assert.Equal(t, 1, BackendAbsent("docker", "daemon down"))
	})
	t.Run("carved -> skip", func(t *testing.T) {
		t.Setenv(uncontrolledBackendsEnv, "docker")
		assert.Equal(t, 0, BackendAbsent("docker", "daemon down"))
	})
	t.Run("a different backend carved -> still fail", func(t *testing.T) {
		t.Setenv(uncontrolledBackendsEnv, "containerd,apple")
		assert.Equal(t, 1, BackendAbsent("docker", "daemon down"))
	})
}
