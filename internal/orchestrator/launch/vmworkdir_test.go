// ABOUTME: Tests for the firstlaunch-storm retry behaviour of VM work-dir setup.
package launch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withFastStormRetry shrinks the storm ceiling/interval so retry tests don't
// sleep for real seconds, restoring the originals afterwards.
func withFastStormRetry(t *testing.T) {
	t.Helper()
	origCeiling, origInterval := firstlaunchStormCeiling, stormRetryInterval
	firstlaunchStormCeiling = 200 * time.Millisecond
	stormRetryInterval = 1 * time.Millisecond
	t.Cleanup(func() {
		firstlaunchStormCeiling = origCeiling
		stormRetryInterval = origInterval
	})
}

func TestIsFirstlaunchStormTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"license storm (exit 69)", &runtime.ExecError{ExitCode: 69, Stderr: "You have not agreed to the Xcode license agreements."}, true},
		{"command not found (exit 127)", &runtime.ExecError{ExitCode: 127, Stderr: "git: command not found"}, true},
		{"exit 69 without license text", &runtime.ExecError{ExitCode: 69, Stderr: "some other failure"}, false},
		{"ordinary non-zero exit", &runtime.ExecError{ExitCode: 1, Stderr: "fatal: not a git repository"}, false},
		{"non-exec error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isFirstlaunchStormTransient(tc.err))
		})
	}
}

func TestExecVMSetupWithStormRetry_SucceedsAfterTransientFailures(t *testing.T) {
	withFastStormRetry(t)
	calls := 0
	result, err := execVMSetupWithStormRetry(context.Background(), func() (runtime.ExecResult, error) {
		calls++
		if calls < 3 {
			return runtime.ExecResult{ExitCode: 69}, &runtime.ExecError{ExitCode: 69, Stderr: "You have not agreed to the Xcode license agreements."}
		}
		return runtime.ExecResult{Stdout: "deadbeef", ExitCode: 0}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "deadbeef", result.Stdout)
	assert.Equal(t, 3, calls, "should retry until the transient storm clears")
}

func TestExecVMSetupWithStormRetry_ReturnsNonTransientImmediately(t *testing.T) {
	withFastStormRetry(t)
	calls := 0
	realErr := &runtime.ExecError{ExitCode: 128, Stderr: "fatal: not a git repository"}
	_, err := execVMSetupWithStormRetry(context.Background(), func() (runtime.ExecResult, error) {
		calls++
		return runtime.ExecResult{ExitCode: 128}, realErr
	})
	assert.ErrorIs(t, err, realErr)
	assert.Equal(t, 1, calls, "non-transient errors must not be retried")
}

func TestExecVMSetupWithStormRetry_GivesUpAtCeiling(t *testing.T) {
	withFastStormRetry(t)
	calls := 0
	stormErr := &runtime.ExecError{ExitCode: 69, Stderr: "You have not agreed to the Xcode license agreements."}
	_, err := execVMSetupWithStormRetry(context.Background(), func() (runtime.ExecResult, error) {
		calls++
		return runtime.ExecResult{ExitCode: 69}, stormErr
	})
	assert.ErrorIs(t, err, stormErr, "the last transient error surfaces when the ceiling elapses")
	assert.Greater(t, calls, 1, "should have retried before giving up")
}

func TestExecVMSetupWithStormRetry_HonorsContextCancellation(t *testing.T) {
	withFastStormRetry(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	stormErr := &runtime.ExecError{ExitCode: 69, Stderr: "You have not agreed to the Xcode license agreements."}
	_, err := execVMSetupWithStormRetry(ctx, func() (runtime.ExecResult, error) {
		calls++
		return runtime.ExecResult{ExitCode: 69}, stormErr
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls, "a cancelled context stops further retries after the first attempt")
}
