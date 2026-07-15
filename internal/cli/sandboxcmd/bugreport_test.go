// ABOUTME: Tests for bug-report section writers: JSON/plain file sections
// ABOUTME: with safe-mode key omission, JSONL event filtering and large-
// ABOUTME: dump tailing, and the monitor detector-decision tail (DF4/DF8).
package sandboxcmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- writeJSONFileSection ---

func TestWriteJSONFileSection_NotFound(t *testing.T) {
	var buf bytes.Buffer
	writeJSONFileSection(&buf, "test.json", "/nonexistent/path/test.json", "safe", nil)
	assert.Contains(t, buf.String(), "not found")
}

func TestWriteJSONFileSection_UnsafeFullContent(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"api_key":"secret"}`), 0600))

	var buf bytes.Buffer
	writeJSONFileSection(&buf, "test.json", path, "unsafe", []string{"api_key"})
	assert.Contains(t, buf.String(), "secret")
}

func TestWriteJSONFileSection_SafeOmitsKey(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"api_key":"secret","name":"test"}`), 0600))

	var buf bytes.Buffer
	writeJSONFileSection(&buf, "test.json", path, "safe", []string{"api_key"})
	out := buf.String()
	assert.NotContains(t, out, "api_key")
	assert.Contains(t, out, "test")
}

func TestWriteJSONFileSection_SafeNoOmitKeys(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"api_key":"secret","name":"test"}`), 0600))

	var buf bytes.Buffer
	writeJSONFileSection(&buf, "test.json", path, "safe", nil)
	out := buf.String()
	// With empty omitKeys, safe mode shows full content
	assert.Contains(t, out, "secret")
	assert.Contains(t, out, "test")
}

// --- writePlainFileSection ---

func TestWritePlainFileSection_NotFound(t *testing.T) {
	var buf bytes.Buffer
	writePlainFileSection(&buf, "missing.txt", "/nonexistent/path/missing.txt")
	assert.Contains(t, buf.String(), "not found")
}

func TestWritePlainFileSection_Found(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "data.txt")
	require.NoError(t, os.WriteFile(path, []byte("file content here"), 0600))

	var buf bytes.Buffer
	writePlainFileSection(&buf, "data.txt", path)
	assert.Contains(t, buf.String(), "file content here")
}

// --- writeBugReportJSONLFile ---

func TestWriteBugReportJSONLFile_NotFound(t *testing.T) {
	var buf bytes.Buffer
	writeBugReportJSONLFile(&buf, "logs/cli.jsonl", "/nonexistent/cli.jsonl", "safe", nil)
	assert.Contains(t, buf.String(), "not found or unreadable")
}

func TestWriteBugReportJSONLFile_OmitsEvents(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sandbox.jsonl")
	line := `{"event":"network.allow","msg":"allowing domain"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0600))

	var buf bytes.Buffer
	writeBugReportJSONLFile(&buf, "logs/sandbox.jsonl", path, "safe", []string{"network.allow"})
	// The event line should be filtered out, only the code block markers remain
	assert.NotContains(t, buf.String(), "allowing domain")
}

func TestWriteBugReportJSONLFile_IncludesAll(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sandbox.jsonl")
	line := `{"event":"network.allow","msg":"allowing domain"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0600))

	var buf bytes.Buffer
	// unsafe mode with no omitEvents → all events included
	writeBugReportJSONLFile(&buf, "logs/sandbox.jsonl", path, "unsafe", nil)
	assert.Contains(t, buf.String(), "allowing domain")
}

func TestWriteBugReportJSONLFile_UnsafePreservesPathsAndIDs(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "sandbox.jsonl")
	// A filesystem path and a 64-hex container id — both prime diagnostic data
	// that the regex sanitizer would eat. Unsafe mode must keep them verbatim.
	line := `{"event":"start","msg":"workdir /Users/me/Projects/yoloai/internal/cli cid e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0600))

	var buf bytes.Buffer
	writeBugReportJSONLFile(&buf, "logs/sandbox.jsonl", path, "unsafe", nil)
	out := buf.String()
	assert.Contains(t, out, "/Users/me/Projects/yoloai/internal/cli")
	assert.Contains(t, out, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.NotContains(t, out, "[REDACTED]")
}

func TestWriteBugReportJSONLFile_TailsLargeDump(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "monitor.jsonl")
	var sb bytes.Buffer
	for i := 0; i < maxJSONLDumpLines+50; i++ {
		fmt.Fprintf(&sb, `{"event":"detector.result","msg":"entry %d"}`+"\n", i)
	}
	require.NoError(t, os.WriteFile(path, sb.Bytes(), 0600))

	var buf bytes.Buffer
	writeBugReportJSONLFile(&buf, "logs/monitor.jsonl", path, "unsafe", nil)
	out := buf.String()
	assert.Contains(t, out, fmt.Sprintf("showing last %d of %d lines", maxJSONLDumpLines, maxJSONLDumpLines+50))
	// Most-recent entry kept; earliest dropped.
	assert.Contains(t, out, fmt.Sprintf("entry %d", maxJSONLDumpLines+49))
	assert.NotContains(t, out, `"entry 0"`)
}

func TestTailLines(t *testing.T) {
	t.Run("under cap returns unchanged", func(t *testing.T) {
		data := []byte("a\nb\nc\n")
		out, omitted := tailLines(data, 10)
		assert.Equal(t, 0, omitted)
		assert.Equal(t, data, out)
	})
	t.Run("over cap keeps the tail", func(t *testing.T) {
		data := []byte("a\nb\nc\nd\n")
		out, omitted := tailLines(data, 2)
		assert.Equal(t, 2, omitted)
		assert.Equal(t, "c\nd\n", string(out))
	})
}

// --- writeBugReportMonitorTail (DF4) -----------------------------------------

func TestWriteBugReportMonitorTail_NotFound(t *testing.T) {
	var buf bytes.Buffer
	writeBugReportMonitorTail(&buf, "/nonexistent/monitor.jsonl")
	assert.Contains(t, buf.String(), "Recent detector decisions")
	assert.Contains(t, buf.String(), "not found")
}

func TestWriteBugReportMonitorTail_NoDetectorEntries(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "monitor.jsonl")
	// Only non-detector events.
	contents := `{"ts":"2026-05-26T14:00:00.000Z","event":"monitor.start","msg":"started"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))

	var buf bytes.Buffer
	writeBugReportMonitorTail(&buf, path)
	assert.Contains(t, buf.String(), "no detector.result entries")
}

func TestWriteBugReportMonitorTail_SurfacesWchanSignal(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "monitor.jsonl")
	// Realistic mix: one startup event + several detector.result entries.
	// The wchan line is the decisive DF8 signal we want surfaced.
	contents := `{"ts":"2026-05-26T14:00:00.000Z","event":"monitor.start","msg":"started"}
{"ts":"2026-05-26T14:00:01.000Z","level":"debug","event":"detector.result","msg":"  hook: file says idle (age=2s) but source='monitor', waiting"}
{"ts":"2026-05-26T14:00:02.000Z","level":"debug","event":"detector.result","msg":"  wchan: do_epoll_wait + no connections -> idle"}
{"ts":"2026-05-26T14:00:03.000Z","level":"debug","event":"detector.result","msg":"pid=38 [hook=unknown wchan=idle(high 5/1)] -> idle (by wchan)"}
`
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))

	var buf bytes.Buffer
	writeBugReportMonitorTail(&buf, path)
	out := buf.String()
	assert.Contains(t, out, "Recent detector decisions")
	assert.Contains(t, out, "wchan: do_epoll_wait + no connections -> idle")
	assert.Contains(t, out, "Last 3 of 3 entries")
	// monitor.start (non-detector) should NOT appear in the tail.
	assert.NotContains(t, out, "monitor.start")
}

func TestWriteBugReportMonitorTail_LimitsToN(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "monitor.jsonl")
	// Write more entries than the tail limit. monitorTailLines == 30.
	var sb bytes.Buffer
	for i := 0; i < monitorTailLines+10; i++ {
		fmt.Fprintf(&sb, `{"ts":"2026-05-26T14:00:%02d.000Z","event":"detector.result","msg":"entry %d"}`+"\n", i, i)
	}
	require.NoError(t, os.WriteFile(path, sb.Bytes(), 0600))

	var buf bytes.Buffer
	writeBugReportMonitorTail(&buf, path)
	out := buf.String()
	// Header records the tail size vs total.
	assert.Contains(t, out, fmt.Sprintf("Last %d of %d entries", monitorTailLines, monitorTailLines+10))
	// Most-recent entries are present; earliest entries dropped.
	assert.Contains(t, out, "entry 39")
	assert.NotContains(t, out, `"entry 0"`)
}
