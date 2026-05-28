// ABOUTME: Tests for buildAndStart's secrets-consumed wait — the marker-based
// ABOUTME: replacement for the fixed sleep that raced on slow Kata VM boot.

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// TestEffectiveSecretsConsumedTimeout verifies the host honors a backend's
// declared wait budget (slow-booting backends raise it so the secrets dir
// isn't removed before the guest reads it) and falls back to the package
// default otherwise.
func TestEffectiveSecretsConsumedTimeout(t *testing.T) {
	assert.Equal(t, secretsConsumedTimeout, effectiveSecretsConsumedTimeout(runtime.BackendDescriptor{}),
		"no backend override → package default")
	assert.Equal(t, 90*time.Second, effectiveSecretsConsumedTimeout(runtime.BackendDescriptor{SecretsConsumedTimeout: 90 * time.Second}),
		"backend-declared cap is honored")
}

// TestWaitForSecretsConsumed_ReturnsWhenMarkerExists verifies the wait
// completes promptly once the marker the in-sandbox entrypoint writes
// appears — the path that lets the host remove the secrets temp dir only
// after the guest has read it.
func TestWaitForSecretsConsumed_ReturnsWhenMarkerExists(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed")
	require.NoError(t, os.WriteFile(marker, nil, 0600))

	start := time.Now()
	waitForSecretsConsumed(marker, 5*time.Second)
	assert.Less(t, time.Since(start), time.Second,
		"should return almost immediately when the marker is already present")
}

// TestWaitForSecretsConsumed_ReturnsWhenMarkerAppears verifies the poll
// observes a marker written after the wait starts (the real ordering: the
// guest boots, reads secrets, then writes the marker while the host polls).
func TestWaitForSecretsConsumed_ReturnsWhenMarkerAppears(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed")

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.WriteFile(marker, nil, 0600)
	}()

	start := time.Now()
	waitForSecretsConsumed(marker, 5*time.Second)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "should have waited for the marker")
	assert.Less(t, elapsed, 2*time.Second, "should return soon after the marker appears")
}

// TestWaitForSecretsConsumed_TimesOut verifies the wait gives up after the
// timeout rather than blocking forever — the safety net that guarantees the
// ephemeral secrets dir is always removed even if a guest never signals.
func TestWaitForSecretsConsumed_TimesOut(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed") // never created

	start := time.Now()
	waitForSecretsConsumed(marker, 250*time.Millisecond)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "should wait out the full timeout")
	assert.Less(t, elapsed, 2*time.Second, "should not block much past the timeout")
}
