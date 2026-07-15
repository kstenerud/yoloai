// ABOUTME: Tests for log-command internals: --since parsing (duration or
// ABOUTME: clock time), level-code formatting, --source key parsing, JSONL
// ABOUTME: record parsing with timestamp fallback, and line formatting.
package sandboxcmd

import (
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSince_Duration(t *testing.T) {
	before := time.Now().UTC()
	got, err := parseSince("5m")
	after := time.Now().UTC()
	require.NoError(t, err)

	// Result should be ~5 minutes before now.
	assert.True(t, got.After(before.Add(-5*time.Minute-time.Second)))
	assert.True(t, got.Before(after.Add(-5*time.Minute+time.Second)))
}

func TestParseSince_TimeHHMMSS(t *testing.T) {
	now := time.Now()
	got, err := parseSince("00:00:00")
	require.NoError(t, err)

	// Should be today at midnight local time.
	expected := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).UTC()
	assert.Equal(t, expected, got)
}

func TestParseSince_TimeHHMM(t *testing.T) {
	now := time.Now()
	got, err := parseSince("00:00")
	require.NoError(t, err)

	expected := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).UTC()
	assert.Equal(t, expected, got)
}

func TestParseSince_Invalid(t *testing.T) {
	for _, bad := range []string{"yesterday", "2026-03-15", "noon", ""} {
		_, err := parseSince(bad)
		assert.Error(t, err, "expected error for %q", bad)
	}
}

// --- levelCode tests ---

func TestLevelCode_KnownLevels(t *testing.T) {
	assert.Equal(t, "DBUG", levelCode("debug"))
	assert.Equal(t, "INFO", levelCode("info"))
	assert.Equal(t, "WARN", levelCode("warn"))
	assert.Equal(t, "WARN", levelCode("warning"))
	assert.Equal(t, "ERRO", levelCode("error"))
}

func TestLevelCode_CaseInsensitive(t *testing.T) {
	assert.Equal(t, "INFO", levelCode("INFO"))
	assert.Equal(t, "INFO", levelCode("Info"))
}

func TestLevelCode_UnknownLong(t *testing.T) {
	// Unknown levels longer than 4 chars are truncated.
	got := levelCode("critical")
	assert.Equal(t, "CRIT", got)
	assert.Len(t, got, 4)
}

func TestLevelCode_UnknownShort(t *testing.T) {
	// Unknown levels shorter than 4 chars are left-padded to 4.
	got := levelCode("ok")
	assert.Len(t, got, 4)
	assert.Contains(t, got, "OK")
}

// --- parseSourceFlag tests ---

func TestParseSourceFlag_Empty_ReturnsNilMeaningAll(t *testing.T) {
	// Empty flag yields nil; the library treats nil Sources as "all sources".
	assert.Nil(t, parseSourceFlag(""))
}

func TestParseSourceFlag_SingleKey(t *testing.T) {
	sources := parseSourceFlag("cli")
	require.Len(t, sources, 1)
	assert.Equal(t, yoloai.LogSourceCLI, sources[0])
}

func TestParseSourceFlag_MultipleKeys(t *testing.T) {
	sources := parseSourceFlag("cli,sandbox")
	require.Len(t, sources, 2)
	assert.ElementsMatch(t, []yoloai.LogSource{yoloai.LogSourceCLI, yoloai.LogSourceSandbox}, sources)
}

func TestParseSourceFlag_WithSpaces(t *testing.T) {
	sources := parseSourceFlag("cli, hooks")
	require.Len(t, sources, 2)
}

func TestParseSourceFlag_UnknownKeyIgnored(t *testing.T) {
	sources := parseSourceFlag("cli,nonexistent")
	require.Len(t, sources, 1)
	assert.Equal(t, yoloai.LogSourceCLI, sources[0])
}

// --- parseLogRecord edge-case tests ---

// cliLabel is the display label parseLogRecord stamps onto records in tests.
var cliLabel = sourceLabels[yoloai.LogSourceCLI]

func TestParseLogRecord_RFC3339Timestamp(t *testing.T) {
	line := `{"ts":"2026-03-15T14:23:01Z","level":"info","event":"e","msg":"m"}`
	rec, ok := parseLogRecord(line, cliLabel)
	require.True(t, ok)
	assert.Equal(t, 2026, rec.timestamp.Year())
	assert.Equal(t, time.March, rec.timestamp.Month())
	assert.Equal(t, 15, rec.timestamp.Day())
}

func TestParseLogRecord_MissingTimestamp(t *testing.T) {
	before := time.Now().UTC()
	line := `{"level":"info","event":"e","msg":"no ts field"}`
	rec, ok := parseLogRecord(line, cliLabel)
	after := time.Now().UTC()
	require.True(t, ok)
	// Fallback: ts is set to time.Now() during parsing.
	assert.True(t, rec.timestamp.Equal(before) || rec.timestamp.After(before))
	assert.True(t, rec.timestamp.Equal(after) || rec.timestamp.Before(after))
}

func TestParseLogRecord_InvalidTimestamp(t *testing.T) {
	before := time.Now().UTC()
	line := `{"ts":"not-a-time","level":"info","event":"e","msg":"m"}`
	rec, ok := parseLogRecord(line, cliLabel)
	after := time.Now().UTC()
	require.True(t, ok)
	// Unparseable ts also falls back to time.Now().
	assert.True(t, rec.timestamp.Equal(before) || rec.timestamp.After(before))
	assert.True(t, rec.timestamp.Equal(after) || rec.timestamp.Before(after))
}

func TestParseLogRecord_NonStringExtraFields(t *testing.T) {
	line := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"e","msg":"m","count":42,"ok":true}`
	rec, ok := parseLogRecord(line, cliLabel)
	require.True(t, ok)

	extraMap := make(map[string]string)
	for _, kv := range rec.extra {
		extraMap[kv[0]] = kv[1]
	}
	assert.Equal(t, "42", extraMap["count"])
	assert.Equal(t, "true", extraMap["ok"])
}

func TestParseLogRecord_InvalidJSON(t *testing.T) {
	_, ok := parseLogRecord("not json at all", cliLabel)
	assert.False(t, ok)

	_, ok = parseLogRecord("{incomplete", cliLabel)
	assert.False(t, ok)
}

// --- formatRecord tests ---

func TestFormatRecord_ContainsExpectedParts(t *testing.T) {
	rec := logRecord{
		timestamp:   time.Date(2026, 3, 15, 14, 23, 1, 0, time.UTC),
		level:       "info",
		event:       "sandbox.create",
		msg:         "creating sandbox",
		sourceLabel: cliLabel,
	}
	out := formatRecord(rec, 0) // width=0 disables truncation
	assert.Contains(t, out, "INFO")
	assert.Contains(t, out, "sandbox.create")
	assert.Contains(t, out, "creating sandbox")
	assert.Contains(t, out, rec.timestamp.Local().Format("15:04:05"))
}

func TestFormatRecord_ExtraFieldsAppended(t *testing.T) {
	rec := logRecord{
		timestamp:   time.Now(),
		level:       "info",
		event:       "e",
		msg:         "msg",
		sourceLabel: cliLabel,
		extra:       [][2]string{{"sandbox", "my-box"}, {"agent", "claude"}},
	}
	out := formatRecord(rec, 0)
	assert.Contains(t, out, "sandbox=my-box")
	assert.Contains(t, out, "agent=claude")
}

func TestFormatRecord_TruncatesAtWidth(t *testing.T) {
	rec := logRecord{
		timestamp:   time.Now(),
		level:       "info",
		event:       "e",
		msg:         strings.Repeat("x", 200),
		sourceLabel: cliLabel,
	}
	out := formatRecord(rec, 80)
	assert.LessOrEqual(t, len(out), 80)
}

func TestFormatRecord_EventPaddedTo24(t *testing.T) {
	rec := logRecord{
		timestamp:   time.Now(),
		level:       "info",
		event:       "short",
		msg:         "msg",
		sourceLabel: cliLabel,
	}
	out := formatRecord(rec, 0)
	// The event column is padded to 24 chars; "short" + 19 spaces should appear.
	assert.Contains(t, out, "short                  ")
}
