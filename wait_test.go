// ABOUTME: Unit tests for Sandbox.Wait's decision logic (waitConditionMet) and
// ABOUTME: its backend-independent polling loop (pollUntil): condition matching,
// ABOUTME: timeout → (lastInfo, ErrWaitTimeout), and ctx-cancel behavior.

package yoloai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitConditionMet(t *testing.T) {
	cases := []struct {
		status    Status
		exit      bool // expected under WaitForExit
		idle      bool // expected under WaitForIdle
		statusStr string
	}{
		{StatusActive, false, false, "active"},
		{StatusIdle, false, true, "idle"},
		{StatusDone, true, true, "done"},
		{StatusFailed, true, true, "failed"},
		{StatusStopped, true, true, "stopped"},
		{StatusSuspended, true, true, "suspended"},
		{StatusRemoved, true, true, "removed"},
		{StatusBroken, true, true, "broken"},
		{StatusUnavailable, true, true, "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.statusStr, func(t *testing.T) {
			assert.Equal(t, tc.exit, waitConditionMet(tc.status, WaitForExit), "WaitForExit")
			assert.Equal(t, tc.idle, waitConditionMet(tc.status, WaitForIdle), "WaitForIdle")
		})
	}
}

// fixedPoll returns a poll func that always reports the given status.
func fixedPoll(status Status) func(context.Context) (*SandboxInfo, error) {
	return func(context.Context) (*SandboxInfo, error) {
		return &SandboxInfo{Status: status}, nil
	}
}

// seqPoll returns a poll func that reports each status in order, repeating the
// last one once exhausted.
func seqPoll(statuses ...Status) func(context.Context) (*SandboxInfo, error) {
	i := 0
	return func(context.Context) (*SandboxInfo, error) {
		s := statuses[i]
		if i < len(statuses)-1 {
			i++
		}
		return &SandboxInfo{Status: s}, nil
	}
}

func TestPollUntil_ReturnsImmediatelyWhenAlreadyTerminal(t *testing.T) {
	info, err := pollUntil(context.Background(), time.Millisecond, 0, WaitForExit, fixedPoll(StatusDone))
	require.NoError(t, err)
	assert.Equal(t, StatusDone, info.Status)
}

func TestPollUntil_TransitionsAfterSeveralPolls(t *testing.T) {
	info, err := pollUntil(context.Background(), time.Millisecond, time.Second, WaitForExit,
		seqPoll(StatusActive, StatusActive, StatusDone))
	require.NoError(t, err)
	assert.Equal(t, StatusDone, info.Status)
}

func TestPollUntil_WaitForExitIgnoresIdle_WaitForIdleStops(t *testing.T) {
	// WaitForExit must not settle on Idle — it times out instead.
	info, err := pollUntil(context.Background(), time.Millisecond, 20*time.Millisecond, WaitForExit, fixedPoll(StatusIdle))
	require.ErrorIs(t, err, ErrWaitTimeout)
	assert.Equal(t, StatusIdle, info.Status, "timeout returns the last-observed info")

	// WaitForIdle settles on Idle immediately.
	info, err = pollUntil(context.Background(), time.Millisecond, time.Second, WaitForIdle, fixedPoll(StatusIdle))
	require.NoError(t, err)
	assert.Equal(t, StatusIdle, info.Status)
}

func TestPollUntil_TimeoutReturnsLastInfoAndWrapsDeadline(t *testing.T) {
	info, err := pollUntil(context.Background(), time.Millisecond, 20*time.Millisecond, WaitForExit, fixedPoll(StatusActive))
	require.ErrorIs(t, err, ErrWaitTimeout)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "ErrWaitTimeout wraps context.DeadlineExceeded")
	require.NotNil(t, info)
	assert.Equal(t, StatusActive, info.Status)
}

func TestPollUntil_CtxCancelReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	info, err := pollUntil(ctx, time.Millisecond, 0, WaitForExit, fixedPoll(StatusActive))
	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrWaitTimeout)
	assert.Nil(t, info, "ctx cancel returns no info")
}

func TestPollUntil_PollErrorPropagates(t *testing.T) {
	sentinel := errors.New("inspect blew up")
	_, err := pollUntil(context.Background(), time.Millisecond, 0, WaitForExit,
		func(context.Context) (*SandboxInfo, error) { return nil, sentinel })
	assert.ErrorIs(t, err, sentinel)
}

func TestErrWaitTimeout_WrapsDeadlineExceeded(t *testing.T) {
	assert.ErrorIs(t, ErrWaitTimeout, context.DeadlineExceeded)
}
