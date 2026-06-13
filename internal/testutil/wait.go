// ABOUTME: Poll-loop helpers (Wait, WaitForStatus, WaitForActive, WaitForStopped)
// ABOUTME: for blocking integration tests until a sandbox or container reaches a state.
package testutil

import (
	"context"
	"errors"
	"testing"
	"time"

	yrt "github.com/kstenerud/yoloai/internal/runtime"
)

// defaultInspectPoll is how often the runtime-state Wait helpers re-inspect
// their target. 200ms balances responsiveness against churn on the runtime
// API; reduce it if a test grows flaky from missing transient states.
const defaultInspectPoll = 200 * time.Millisecond

// Wait polls fetch at pollInterval until cond returns true or timeout elapses.
// On timeout, fails the test with t.Fatalf("%s within %s", failMsg, timeout).
//
// This is the shared poll-loop primitive used by WaitForActive and
// WaitForStopped. Tests that need a custom condition can call it directly
// instead of writing their own deadline loop.
func Wait[T any](
	ctx context.Context,
	t *testing.T,
	failMsg string,
	fetch func(context.Context) (T, error),
	cond func(T, error) bool,
	timeout, pollInterval time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		v, err := fetch(ctx)
		if cond(v, err) {
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("%s within %s", failMsg, timeout)
}

// WaitForStatus polls statusFn until it returns want or timeout elapses.
// Pass a closure that calls sandbox.DetectStatus (or any other status source)
// rather than a Runtime directly — sandbox status is a higher-level concept
// than runtime running/stopped state.
//
// Kept as a bespoke loop (rather than a Wait wrapper) so the "last seen"
// value can be threaded into the failure message — Wait's static failMsg
// can't capture state observed during the loop.
//
// Example:
//
//	testutil.WaitForStatus(ctx, t, func(ctx context.Context) (string, error) {
//	    s, err := sandbox.DetectStatus(ctx, rt, sandbox.InstanceName(name), sandbox.Dir(name))
//	    return string(s), err
//	}, string(sandbox.StatusDone), 30*time.Second)
func WaitForStatus(ctx context.Context, t *testing.T, statusFn func(context.Context) (string, error), want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		got, err := statusFn(ctx)
		if err == nil {
			last = got
			if got == want {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("status never became %q within %s (last seen: %q)", want, timeout, last)
}

// WaitForActive polls rt.Inspect until the instance is running or timeout elapses.
// instanceName must already be the resolved runtime instance name (e.g. sandbox.InstanceName(name)).
func WaitForActive(ctx context.Context, t *testing.T, rt yrt.Backend, instanceName string, timeout time.Duration) {
	t.Helper()
	Wait(
		ctx, t,
		"instance "+instanceName+" never became active",
		func(ctx context.Context) (yrt.InstanceInfo, error) { return rt.Inspect(ctx, instanceName) },
		func(info yrt.InstanceInfo, err error) bool { return err == nil && info.Running },
		timeout, defaultInspectPoll,
	)
}

// WaitForStopped polls rt.Inspect until the instance is not running (stopped or removed)
// or timeout elapses.
// instanceName must already be the resolved runtime instance name.
func WaitForStopped(ctx context.Context, t *testing.T, rt yrt.Backend, instanceName string, timeout time.Duration) {
	t.Helper()
	Wait(
		ctx, t,
		"instance "+instanceName+" never stopped",
		func(ctx context.Context) (yrt.InstanceInfo, error) { return rt.Inspect(ctx, instanceName) },
		func(info yrt.InstanceInfo, err error) bool {
			return errors.Is(err, yrt.ErrNotFound) || (err == nil && !info.Running)
		},
		timeout, defaultInspectPoll,
	)
}
