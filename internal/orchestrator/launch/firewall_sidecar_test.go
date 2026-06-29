// ABOUTME: Tests for the bounded retry around the network-isolation firewall
// ABOUTME: sidecar install — it must recover from a transient container-runtime
// ABOUTME: hiccup yet still fail closed when the install is persistently broken.
package launch

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
)

// fakeSidecarRunner records how many times RunNetnsSidecar is called and fails
// the first failUntil-1 calls, succeeding on the failUntil-th. A failUntil of 0
// means every call fails.
type fakeSidecarRunner struct {
	calls     int
	failUntil int
	err       error
}

func (f *fakeSidecarRunner) RunNetnsSidecar(_ context.Context, _ runtime.NetnsSidecarSpec) error {
	f.calls++
	if f.failUntil == 0 || f.calls < f.failUntil {
		return f.err
	}
	return nil
}

func TestRunNetnsSidecarWithRetry_SucceedsFirstTry(t *testing.T) {
	r := &fakeSidecarRunner{failUntil: 1, err: errors.New("boom")}
	err := runNetnsSidecarWithRetry(context.Background(), r, runtime.NetnsSidecarSpec{}, "sb")
	require.NoError(t, err)
	assert.Equal(t, 1, r.calls, "happy path must not retry")
}

func TestRunNetnsSidecarWithRetry_RecoversFromTransientFailure(t *testing.T) {
	// Fails attempt 1, succeeds attempt 2 — the transient-hiccup case.
	r := &fakeSidecarRunner{failUntil: 2, err: errors.New("rootfs not ready")}
	err := runNetnsSidecarWithRetry(context.Background(), r, runtime.NetnsSidecarSpec{}, "sb")
	require.NoError(t, err)
	assert.Equal(t, 2, r.calls)
}

func TestRunNetnsSidecarWithRetry_FailsClosedAfterAllAttempts(t *testing.T) {
	wantErr := errors.New("genuinely broken")
	r := &fakeSidecarRunner{failUntil: 0, err: wantErr}
	err := runNetnsSidecarWithRetry(context.Background(), r, runtime.NetnsSidecarSpec{}, "sb")
	require.ErrorIs(t, err, wantErr, "persistent failure must surface (fail closed)")
	assert.Equal(t, firewallSidecarAttempts, r.calls, "must exhaust all attempts")
}

func TestRunNetnsSidecarWithRetry_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &fakeSidecarRunner{failUntil: 0, err: errors.New("boom")}
	err := runNetnsSidecarWithRetry(ctx, r, runtime.NetnsSidecarSpec{}, "sb")
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, r.calls, "a cancelled context must abort before the second attempt")
}
