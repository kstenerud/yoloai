package testutil

import (
	"context"
	"errors"
	"testing"
	"time"

	yrt "github.com/kstenerud/yoloai/runtime"
)

// WaitForStatus polls statusFn until it returns want or timeout elapses.
// Pass a closure that calls sandbox.DetectStatus (or any other status source)
// rather than a Runtime directly — sandbox status is a higher-level concept
// than runtime running/stopped state.
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
func WaitForActive(ctx context.Context, t *testing.T, rt yrt.Runtime, instanceName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := rt.Inspect(ctx, instanceName)
		if err == nil && info.Running {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("instance %q never became active within %s", instanceName, timeout)
}

// WaitForStopped polls rt.Inspect until the instance is not running (stopped or removed)
// or timeout elapses.
// instanceName must already be the resolved runtime instance name.
func WaitForStopped(ctx context.Context, t *testing.T, rt yrt.Runtime, instanceName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := rt.Inspect(ctx, instanceName)
		if errors.Is(err, yrt.ErrNotFound) || (err == nil && !info.Running) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("instance %q never stopped within %s", instanceName, timeout)
}
