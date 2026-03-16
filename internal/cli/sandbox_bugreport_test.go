package cli

import (
	"bytes"
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
