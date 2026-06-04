package bugreport

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- vmSlotLine ---

func TestVMSlotLine(t *testing.T) {
	tests := []struct {
		name string
		slot yoloai.VMSlot
		want string
	}{
		{"owned", yoloai.VMSlot{PID: 100, VMName: "alpha", Owned: true}, "pid 100  alpha — owned sandbox"},
		{"orphan", yoloai.VMSlot{PID: 200, VMName: "ghost"}, "pid 200  ghost — orphan (launcher gone), holding a slot"},
		{"orphan deleted", yoloai.VMSlot{PID: 300, VMName: "tmp", Deleted: true}, "pid 300  tmp — orphan (image deleted), holding a slot"},
		{"unknown name", yoloai.VMSlot{PID: 400}, "pid 400  (unknown) — orphan (launcher gone), holding a slot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, vmSlotLine(tt.slot))
		})
	}
}

// --- Filename ---

func TestBugReportFilename_Format(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)
	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))
	t.Cleanup(func() { _ = os.Chdir(origDir) }) //nolint:gosec // G104: chdir in test cleanup

	name, err := Filename(time.Now())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(name, "yoloai-bugreport-"), "name should start with yoloai-bugreport-")
	assert.True(t, strings.HasSuffix(name, ".md"), "name should end with .md")
	// The PID is embedded so concurrent invocations never collide; locking it
	// here guards the parallel-safety property against an accidental revert.
	assert.Contains(t, name, fmt.Sprintf("-%d.md", os.Getpid()), "name should embed the PID")
}

func TestBugReportFilename_Collision(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)
	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))
	t.Cleanup(func() { _ = os.Chdir(origDir) }) //nolint:gosec // G104: chdir in test cleanup

	ts := time.Date(2026, 3, 16, 5, 20, 52, 931000000, time.UTC)
	name, err := Filename(ts)
	require.NoError(t, err)

	// Create the file
	require.NoError(t, os.WriteFile(name, []byte("content"), 0600))

	// Same timestamp should fail
	_, err = Filename(ts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// --- redactPromptArgs ---

func TestRedactPromptArgs_LongForm(t *testing.T) {
	args := []string{"yoloai", "--prompt", "secret task"}
	result := redactPromptArgs(args)
	assert.Equal(t, "yoloai", result[0])
	assert.Equal(t, "--prompt", result[1])
	assert.Equal(t, "[REDACTED]", result[2])
}

func TestRedactPromptArgs_ShortForm(t *testing.T) {
	args := []string{"yoloai", "-p", "secret task"}
	result := redactPromptArgs(args)
	assert.Equal(t, "-p", result[1])
	assert.Equal(t, "[REDACTED]", result[2])
}

func TestRedactPromptArgs_EqualsForm(t *testing.T) {
	args := []string{"yoloai", "--prompt=secret task"}
	result := redactPromptArgs(args)
	assert.Equal(t, "--prompt=[REDACTED]", result[1])
}

func TestRedactPromptArgs_OtherFlagsUnchanged(t *testing.T) {
	args := []string{"--agent", "claude"}
	result := redactPromptArgs(args)
	assert.Equal(t, "--agent", result[0])
	assert.Equal(t, "claude", result[1])
}

// --- sanitizeYAMLConfig ---

func TestSanitizeYAMLConfig_RedactsAPIKey(t *testing.T) {
	input := []byte("anthropic_api_key: sk-ant-abc123\n")
	result := sanitizeYAMLConfig(input)
	assert.NotContains(t, string(result), "sk-ant-abc123")
	assert.Contains(t, string(result), "[REDACTED]")
}

func TestSanitizeYAMLConfig_RedactsToken(t *testing.T) {
	input := []byte("github_token: ghp_abc123\n")
	result := sanitizeYAMLConfig(input)
	assert.NotContains(t, string(result), "ghp_abc123")
	assert.Contains(t, string(result), "[REDACTED]")
}

func TestSanitizeYAMLConfig_PreservesNonSensitive(t *testing.T) {
	input := []byte("agent: claude\n")
	result := sanitizeYAMLConfig(input)
	assert.Equal(t, "agent: claude\n", string(result))
}

func TestSanitizeYAMLConfig_IndentedKey(t *testing.T) {
	input := []byte("  api_key: abc\n")
	result := sanitizeYAMLConfig(input)
	assert.NotContains(t, string(result), "abc")
	assert.Contains(t, string(result), "[REDACTED]")
}

// --- sanitizeText ---

func TestSanitizeText_APIKeyPrefix(t *testing.T) {
	result := sanitizeText("token sk-ant-abc123xyz")
	assert.NotContains(t, result, "sk-ant-abc123xyz")
	assert.Contains(t, result, "[REDACTED]")
}

func TestSanitizeText_AWSKey(t *testing.T) {
	result := sanitizeText("AKIAIOSFODNN7EXAMPLE")
	assert.NotContains(t, result, "AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, result, "[REDACTED]")
}

func TestSanitizeText_NormalTextPreserved(t *testing.T) {
	result := sanitizeText("hello world")
	assert.Equal(t, "hello world", result)
}

// --- shouldOmitEvent ---

func TestShouldOmitEvent_ExactMatch(t *testing.T) {
	assert.True(t, shouldOmitEvent("network.allow", []string{"network.allow"}))
}

func TestShouldOmitEvent_PrefixMatch(t *testing.T) {
	assert.True(t, shouldOmitEvent("setup_cmd.start", []string{"setup_cmd.*"}))
}

func TestShouldOmitEvent_NoMatch(t *testing.T) {
	assert.False(t, shouldOmitEvent("sandbox.ready", []string{"network.allow", "setup_cmd.*"}))
}

func TestShouldOmitEvent_EmptyPatterns(t *testing.T) {
	assert.False(t, shouldOmitEvent("anything", []string{}))
}

// --- SanitizeJSONLBytes ---

func TestSanitizeJSONLBytes_OmitsEvents(t *testing.T) {
	line := `{"event":"network.allow","msg":"allowing domain"}` + "\n"
	result := SanitizeJSONLBytes([]byte(line), []string{"network.allow"}, true)
	assert.NotContains(t, string(result), "network.allow")
}

func TestSanitizeJSONLBytes_SkipsEmptyLines(t *testing.T) {
	input := `{"event":"a","msg":"hello"}` + "\n\n" + `{"event":"b","msg":"world"}` + "\n"
	result := SanitizeJSONLBytes([]byte(input), nil, true)
	lines := strings.Split(strings.TrimSpace(string(result)), "\n")
	assert.Len(t, lines, 2)
}

func TestSanitizeJSONLBytes_PassesThroughMalformed(t *testing.T) {
	input := "not valid json\n"
	result := SanitizeJSONLBytes([]byte(input), nil, true)
	assert.Contains(t, string(result), "not valid json")
}

func TestSanitizeJSONLBytes_SanitizesAPIKey(t *testing.T) {
	line := `{"event":"test","msg":"key is sk-ant-secret123"}` + "\n"
	result := SanitizeJSONLBytes([]byte(line), nil, true)
	assert.NotContains(t, string(result), "sk-ant-secret123")
	assert.Contains(t, string(result), "[REDACTED]")
}

func TestSanitizeJSONLBytes_NoRedactWhenDisabled(t *testing.T) {
	// Unsafe reports pass redactText=false: the line must survive verbatim so
	// the report is a faithful record.
	line := `{"event":"test","msg":"key is sk-ant-secret123"}` + "\n"
	result := SanitizeJSONLBytes([]byte(line), nil, false)
	assert.Contains(t, string(result), "sk-ant-secret123")
	assert.NotContains(t, string(result), "[REDACTED]")
}

func TestSanitizeJSONLBytes_DisabledStillOmitsEvents(t *testing.T) {
	// Event omission is independent of text redaction.
	line := `{"event":"network.allow","msg":"allowing domain"}` + "\n"
	result := SanitizeJSONLBytes([]byte(line), []string{"network.allow"}, false)
	assert.NotContains(t, string(result), "allowing domain")
}

func TestSanitizeText_PreservesFilesystemPaths(t *testing.T) {
	// A long path must not be collapsed to [REDACTED] — paths are prime
	// diagnostic data. The '/' is what previously triggered the base64 rule.
	path := "/Users/karlstenerud/Projects/yoloai/internal/cli/sandboxcmd"
	assert.Equal(t, path, sanitizeText(path))
}

// --- WriteHeader ---

func TestWriteBugReportHeader_Unsafe(t *testing.T) {
	var buf bytes.Buffer
	WriteHeader(&buf, "1.0.0", "abc1234", "2026-03-16", "unsafe")
	out := buf.String()
	assert.Contains(t, out, "UNSAFE REPORT")
	assert.Contains(t, out, "Do not share publicly")
}

func TestWriteBugReportHeader_Safe(t *testing.T) {
	var buf bytes.Buffer
	WriteHeader(&buf, "1.0.0", "abc1234", "2026-03-16", "safe")
	out := buf.String()
	assert.Contains(t, out, "Review before sharing")
}

func TestWriteBugReportHeader_VersionInfo(t *testing.T) {
	var buf bytes.Buffer
	WriteHeader(&buf, "1.2.3", "deadbeef", "2026-03-16", "safe")
	out := buf.String()
	assert.Contains(t, out, "1.2.3")
	assert.Contains(t, out, "deadbeef")
	assert.Contains(t, out, "2026-03-16")
}

// --- WriteLiveLog ---

func TestWriteBugReportLiveLog_EmptySkipped(t *testing.T) {
	var buf bytes.Buffer
	WriteLiveLog(&buf, []byte("   \n  \n"), "safe")
	assert.Empty(t, buf.String())
}

func TestWriteBugReportLiveLog_ContainsEntry(t *testing.T) {
	var buf bytes.Buffer
	entry := `{"ts":"2026-03-16T05:20:52.000Z","level":"info","event":"test.event","msg":"hello from live log"}` + "\n"
	WriteLiveLog(&buf, []byte(entry), "safe")
	out := buf.String()
	assert.Contains(t, out, "Live log")
	assert.Contains(t, out, "hello from live log")
}

// --- WriteExit ---

func TestWriteBugReportExit_Success(t *testing.T) {
	var buf bytes.Buffer
	WriteExit(&buf, 0, nil, false)
	assert.Contains(t, buf.String(), "Exit code:")
	assert.Contains(t, buf.String(), "0")
}

func TestWriteBugReportExit_Error(t *testing.T) {
	var buf bytes.Buffer
	WriteExit(&buf, 1, fmt.Errorf("something went wrong"), false)
	out := buf.String()
	assert.Contains(t, out, "Exit code:")
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "something went wrong")
}

func TestWriteBugReportExit_Panic(t *testing.T) {
	var buf bytes.Buffer
	WriteExit(&buf, 1, nil, true)
	assert.Contains(t, buf.String(), "(panic)")
}
