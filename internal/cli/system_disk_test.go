package cli

// ABOUTME: Tests for free-space detection and the low-disk warning helper.

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreeBytesAt_ExistingDir(t *testing.T) {
	// Any extant path on a writable filesystem has > 0 free bytes
	// available to an unprivileged user.
	free, err := freeBytesAt(t.TempDir())
	require.NoError(t, err)
	assert.Greater(t, free, int64(0), "expected positive free bytes on tmp filesystem")
}

func TestFreeBytesAt_NonexistentPath_WalksUp(t *testing.T) {
	// Deeply nested path under an existing tmp dir, none of the
	// intermediate components exist. freeBytesAt should walk up
	// until it finds the tmp root.
	base := t.TempDir()
	deep := filepath.Join(base, "a", "b", "c", "d", "e")

	free, err := freeBytesAt(deep)
	require.NoError(t, err)
	assert.Greater(t, free, int64(0))
}

func TestFreeBytesAt_RootAlwaysExists(t *testing.T) {
	// "/" always exists on Linux; this is the worst-case walk-up.
	free, err := freeBytesAt("/")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, free, int64(0))
}

func TestEmitLowDiskWarning_BelowThreshold(t *testing.T) {
	var buf bytes.Buffer
	emitted := emitLowDiskWarning(&buf, "/path/to/data", 100*1024*1024, 2*1024*1024*1024)
	assert.True(t, emitted, "should emit when below threshold")
	out := buf.String()
	// Regress-guard the actionable parts of the message.
	assert.Contains(t, out, "Warning")
	assert.Contains(t, out, "100.0 MiB")
	assert.Contains(t, out, "/path/to/data")
	assert.Contains(t, out, "yoloai system disk")
	assert.Contains(t, out, "yoloai system prune --cache")
}

func TestEmitLowDiskWarning_AboveThreshold(t *testing.T) {
	var buf bytes.Buffer
	emitted := emitLowDiskWarning(&buf, "/path/to/data", 5*1024*1024*1024, 2*1024*1024*1024)
	assert.False(t, emitted, "should not emit when above threshold")
	assert.Empty(t, buf.String())
}

func TestEmitLowDiskWarning_ExactlyAtThreshold(t *testing.T) {
	// free == threshold should NOT warn (off-by-one regress guard).
	var buf bytes.Buffer
	threshold := int64(2 * 1024 * 1024 * 1024)
	emitted := emitLowDiskWarning(&buf, "/", threshold, threshold)
	assert.False(t, emitted)
	assert.Empty(t, buf.String())
}

func TestWarnIfLowDisk_DoesNotCrashOnRealPath(t *testing.T) {
	// Smoke test for the full pipeline: it should not crash on an
	// existing path, and at the const threshold (2 GiB) most CI/dev
	// machines have more free space so we don't assert on output.
	// emitLowDiskWarning's tests above cover the message content.
	var buf bytes.Buffer
	warnIfLowDisk(&buf, t.TempDir())
	// No assertion on buf contents — whether output appears depends
	// on the host's actual free space, which is uncontrollable here.
	// What matters is the function didn't panic.
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		name string
		n    int64
		want string
	}{
		{"bytes", 512, "512 B"},
		{"kib", 1500, "1.5 KiB"},
		{"mib", 5 * 1024 * 1024, "5.0 MiB"},
		{"gib_exact", 2 * 1024 * 1024 * 1024, "2.00 GiB"},
		{"zero", 0, "0 B"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, humanBytes(c.n))
		})
	}
}

// Sanity: the threshold constant should be in a plausible range —
// guards against accidental zero or absurdly-large values from a
// future refactor.
func TestLowDiskThreshold_Sanity(t *testing.T) {
	assert.Greater(t, lowDiskWarnThresholdBytes, int64(100*1024*1024),
		"threshold should be at least 100 MiB to be meaningful")
	assert.Less(t, lowDiskWarnThresholdBytes, int64(100*1024*1024*1024),
		"threshold should be under 100 GiB to avoid false positives on most systems")
}
