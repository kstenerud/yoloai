//go:build e2e

// ABOUTME: The binary's error-path process contract — that a real failure exits
// ABOUTME: non-zero with a human-readable stderr message. The error→exit-code
// ABOUTME: mapping itself is unit-tested (TestErrorExitCode); this is the one
// ABOUTME: e2e smoke proving main()/Execute() wire that mapping to os.Exit.
package e2e_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestE2E_ErrorExitCodeAndMessage drives a real failure through the compiled
// binary: destroying a missing sandbox must exit 1 (the UsageError mapping) and
// name the offending sandbox on stderr. Workflow/duplicate-name behavior is
// owned by the in-process cli integration tier; the mapping table by the unit
// tier — this asserts only what a subprocess uniquely proves.
func TestE2E_ErrorExitCodeAndMessage(t *testing.T) {
	_ = e2eSetup(t)

	_, stderr, code := runYoloai(t, "destroy", "--abandon-unapplied", "no-such-sandbox")
	assert.Equal(t, 1, code, "destroy of a nonexistent sandbox should exit 1")
	assert.Contains(t, stderr, "no-such-sandbox", "error message should name the sandbox")
}
