package testutil

import (
	"context"
	"errors"
	"testing"
	"time"

	yrt "github.com/kstenerud/yoloai/runtime"
)

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
