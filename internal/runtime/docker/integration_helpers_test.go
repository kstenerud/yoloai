//go:build integration

package docker

import (
	"os"
	"strings"
)

// envFromOS snapshots the process environment as a map for TestMain's one-time
// daemon probe. Integration tests are the test-side boundary (equivalent to the
// CLI's licensed os.Environ read), so they thread the real host env into New
// just as the CLI would. The conformance suite uses runtimetest.EnvFromOS for
// the per-test connections.
func envFromOS() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() { //nolint:forbidigo // §12: licensed test-edge env snapshot → layout.Env; curated by the runtime's execEnv allowlist before any subprocess sees it
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}
