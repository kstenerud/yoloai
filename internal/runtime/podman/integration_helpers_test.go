//go:build integration

package podman

import (
	"os"
	"strings"
)

// envFromOS snapshots the process environment as a map for TestMain's one-time
// daemon probe. Integration tests are the test-side boundary (equivalent to the
// CLI's licensed os.Environ read), so they thread the real host env into New /
// discoverSocket just as the CLI would (§12). The conformance suite uses
// runtimetest.EnvFromOS for the per-test connections.
func envFromOS() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}
