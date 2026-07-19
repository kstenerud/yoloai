// ABOUTME: Unit tests for build.go's DF145 error forwarding: a failed tart
// ABOUTME: subprocess (pull, tool verification, imprint write) must carry the
// ABOUTME: tail of its own stderr on the returned error, not a bare exit code.

package tart

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTartRuntime returns a Runtime whose tartBin always fails with the given
// stderr, reusing the fakeFailingTart stub from tart_test.go.
func fakeTartRuntime(t *testing.T, stderr string) *Runtime {
	t.Helper()
	return &Runtime{tartBin: fakeFailingTart(t, stderr), execEnv: []string{"PATH=/usr/bin:/bin"}}
}

func TestPullImage_ErrorCarriesStderrTail(t *testing.T) {
	const cause = `Error: AuthFailed(why: "received unexpected HTTP status code 403")`
	r := fakeTartRuntime(t, cause)

	err := r.pullImage(context.Background(), "ghcr.io/nonexistent/image:latest", io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pull ghcr.io/nonexistent/image:latest",
		"the error names the operation")
	assert.Contains(t, err.Error(), cause,
		"the tart CLI's own diagnostic rides on the error, not only the stream (DF145)")
}

func TestVerifyTools_ErrorCarriesStderrTail(t *testing.T) {
	const cause = "MISSING: gh"
	r := fakeTartRuntime(t, cause)

	err := r.verifyTools(context.Background(), "yoloai-test-vm", io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), cause,
		"the missing-tool name rides on the error, not only the stream (DF145)")
}

func TestWriteImprint_ErrorCarriesStderrTail(t *testing.T) {
	const cause = "bash: cannot create ~/.yoloai-base-info: Read-only file system"
	r := fakeTartRuntime(t, cause)

	err := r.writeImprint(context.Background(), "yoloai-test-vm", "base")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write build imprint", "the error names the operation")
	assert.Contains(t, err.Error(), cause,
		"without a captured tail this exec's stderr was discarded entirely (DF145)")
}
