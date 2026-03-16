package cli

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingHandler records every Handle call for inspection.
type capturingHandler struct {
	minLevel slog.Level
	records  []slog.Record
	attrs    []slog.Attr
	group    string
}

func (h *capturingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := &capturingHandler{minLevel: h.minLevel, attrs: append(h.attrs, attrs...)}
	return n
}

func (h *capturingHandler) WithGroup(name string) slog.Handler {
	return &capturingHandler{minLevel: h.minLevel, group: name}
}

// --- multiSinkHandler tests ---

func TestMultiSinkHandler_FansOutToAllSinks(t *testing.T) {
	sink1 := &capturingHandler{minLevel: slog.LevelDebug}
	sink2 := &capturingHandler{minLevel: slog.LevelDebug}

	m := &multiSinkHandler{}
	m.addSink(sink1, slog.LevelDebug)
	m.addSink(sink2, slog.LevelDebug)

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	require.NoError(t, m.Handle(context.Background(), r))

	assert.Len(t, sink1.records, 1)
	assert.Len(t, sink2.records, 1)
	assert.Equal(t, "hello", sink1.records[0].Message)
	assert.Equal(t, "hello", sink2.records[0].Message)
}

func TestMultiSinkHandler_LevelFilteringPerSink(t *testing.T) {
	debugSink := &capturingHandler{minLevel: slog.LevelDebug}
	errorSink := &capturingHandler{minLevel: slog.LevelError}

	m := &multiSinkHandler{}
	m.addSink(debugSink, slog.LevelDebug)
	m.addSink(errorSink, slog.LevelError)

	info := slog.NewRecord(time.Now(), slog.LevelInfo, "info msg", 0)
	require.NoError(t, m.Handle(context.Background(), info))

	// debugSink receives INFO; errorSink does not.
	assert.Len(t, debugSink.records, 1)
	assert.Len(t, errorSink.records, 0)

	err := slog.NewRecord(time.Now(), slog.LevelError, "error msg", 0)
	require.NoError(t, m.Handle(context.Background(), err))

	// Both sinks receive ERROR.
	assert.Len(t, debugSink.records, 2)
	assert.Len(t, errorSink.records, 1)
}

func TestMultiSinkHandler_Enabled_AnyThreshold(t *testing.T) {
	// One sink at WARN, one at ERROR.
	warnSink := &capturingHandler{minLevel: slog.LevelWarn}
	errorSink := &capturingHandler{minLevel: slog.LevelError}

	m := &multiSinkHandler{}
	m.addSink(warnSink, slog.LevelWarn)
	m.addSink(errorSink, slog.LevelError)

	// INFO is below both — should be disabled.
	assert.False(t, m.Enabled(context.Background(), slog.LevelInfo))
	// WARN satisfies the WARN sink — should be enabled.
	assert.True(t, m.Enabled(context.Background(), slog.LevelWarn))
	// ERROR satisfies both sinks.
	assert.True(t, m.Enabled(context.Background(), slog.LevelError))
}

func TestMultiSinkHandler_Enabled_NoSinks(t *testing.T) {
	m := &multiSinkHandler{}
	assert.False(t, m.Enabled(context.Background(), slog.LevelInfo))
}

func TestMultiSinkHandler_WithAttrs_Propagates(t *testing.T) {
	var buf bytes.Buffer
	m := &multiSinkHandler{}
	m.addSink(newJSONLHandler(&buf, slog.LevelDebug), slog.LevelDebug)

	child := m.WithAttrs([]slog.Attr{slog.String("app", "yoloai")})
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	require.NoError(t, child.Handle(context.Background(), r))

	assert.Contains(t, buf.String(), `"app":"yoloai"`)
}

func TestMultiSinkHandler_WithGroup_Propagates(t *testing.T) {
	var buf bytes.Buffer
	m := &multiSinkHandler{}
	m.addSink(newJSONLHandler(&buf, slog.LevelDebug), slog.LevelDebug)

	child := m.WithGroup("req")
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	r.AddAttrs(slog.String("id", "abc"))
	require.NoError(t, child.Handle(context.Background(), r))

	assert.Contains(t, buf.String(), `"req"`)
}

func TestMultiSinkHandler_Handle_EmptyNoError(t *testing.T) {
	m := &multiSinkHandler{}
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	assert.NoError(t, m.Handle(context.Background(), r))
}

// --- newJSONLHandler wire-format tests ---

func TestJSONLHandler_FieldNames(t *testing.T) {
	var buf bytes.Buffer
	h := newJSONLHandler(&buf, slog.LevelDebug)
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	require.NoError(t, h.Handle(context.Background(), r))

	line := buf.String()
	assert.Contains(t, line, `"ts":`, "timestamp field must be named 'ts'")
	assert.Contains(t, line, `"level":`, "level field must be present")
	assert.Contains(t, line, `"msg":`, "msg field must be present")
	assert.NotContains(t, line, `"time":`, "'time' key must be renamed to 'ts'")
	assert.NotContains(t, line, `"source":`, "'source' key must be omitted")
}

func TestJSONLHandler_LevelLowercase(t *testing.T) {
	var buf bytes.Buffer
	h := newJSONLHandler(&buf, slog.LevelDebug)

	for _, tc := range []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, `"level":"debug"`},
		{slog.LevelInfo, `"level":"info"`},
		{slog.LevelWarn, `"level":"warn"`},
		{slog.LevelError, `"level":"error"`},
	} {
		buf.Reset()
		r := slog.NewRecord(time.Now(), tc.level, "msg", 0)
		require.NoError(t, h.Handle(context.Background(), r))
		assert.Contains(t, buf.String(), tc.want, "level for %v", tc.level)
	}
}

func TestJSONLHandler_TimestampFormat(t *testing.T) {
	var buf bytes.Buffer
	h := newJSONLHandler(&buf, slog.LevelDebug)
	fixed := time.Date(2026, 3, 15, 14, 23, 1, 500_000_000, time.UTC)
	r := slog.NewRecord(fixed, slog.LevelInfo, "msg", 0)
	require.NoError(t, h.Handle(context.Background(), r))

	// Expect RFC3339 milliseconds UTC: 2026-03-15T14:23:01.500Z
	assert.Contains(t, buf.String(), `"ts":"2026-03-15T14:23:01.500Z"`)
}

func TestJSONLHandler_TimestampUTC(t *testing.T) {
	var buf bytes.Buffer
	h := newJSONLHandler(&buf, slog.LevelDebug)
	// Use a non-UTC local time to verify the handler converts to UTC.
	loc := time.FixedZone("UTC+5", 5*60*60)
	nonUTC := time.Date(2026, 3, 15, 19, 0, 0, 0, loc) // same instant as 14:00 UTC
	r := slog.NewRecord(nonUTC, slog.LevelInfo, "msg", 0)
	require.NoError(t, h.Handle(context.Background(), r))

	assert.Contains(t, buf.String(), `"ts":"2026-03-15T14:00:00.000Z"`)
}

func TestJSONLHandler_MinLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	h := newJSONLHandler(&buf, slog.LevelWarn)

	// Enabled() must reflect the min level — callers check Enabled before Handle.
	assert.False(t, h.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, h.Enabled(context.Background(), slog.LevelWarn))
	assert.True(t, h.Enabled(context.Background(), slog.LevelError))

	// Via a Logger, which respects Enabled, only WARN+ should be written.
	logger := slog.New(h)
	logger.Info("should not appear")
	assert.Empty(t, buf.String())

	logger.Warn("should appear")
	assert.Contains(t, buf.String(), "should appear")
}

// --- newTextHandler tests ---

func TestTextHandler_OmitsTimestamp(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, slog.LevelDebug)
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	require.NoError(t, h.Handle(context.Background(), r))

	assert.NotContains(t, buf.String(), "time=", "text handler must omit timestamp")
}

func TestTextHandler_OmitsSource(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, slog.LevelDebug)
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	require.NoError(t, h.Handle(context.Background(), r))

	assert.NotContains(t, buf.String(), "source=", "text handler must omit source")
}

func TestTextHandler_IncludesMessage(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, slog.LevelDebug)
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "watch out", 0)
	require.NoError(t, h.Handle(context.Background(), r))

	assert.Contains(t, buf.String(), "watch out")
}

func TestTextHandler_MinLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	h := newTextHandler(&buf, slog.LevelError)

	// Enabled() must reflect the min level.
	assert.False(t, h.Enabled(context.Background(), slog.LevelWarn))
	assert.True(t, h.Enabled(context.Background(), slog.LevelError))

	// Via a Logger, which respects Enabled, only ERROR+ should be written.
	logger := slog.New(h)
	logger.Warn("ignored")
	assert.Empty(t, buf.String())

	logger.Error("shown")
	assert.Contains(t, buf.String(), "shown")
}

// --- AddLogSink nil-guard test ---

func TestAddLogSink_NilGlobalHandler(t *testing.T) {
	prev := globalHandler
	globalHandler = nil
	t.Cleanup(func() { globalHandler = prev })

	// Must not panic when globalHandler is nil.
	var buf bytes.Buffer
	assert.NotPanics(t, func() { AddLogSink(&buf, slog.LevelDebug) })
	assert.Empty(t, buf.String())
}

func TestAddLogSink_ReceivesRecords(t *testing.T) {
	prevHandler := globalHandler
	prevLogger := slog.Default()
	globalHandler = &multiSinkHandler{}
	t.Cleanup(func() {
		globalHandler = prevHandler
		slog.SetDefault(prevLogger)
	})
	slog.SetDefault(slog.New(globalHandler))

	var buf bytes.Buffer
	AddLogSink(&buf, slog.LevelDebug)
	slog.Info("via sink", "key", "val")

	assert.Contains(t, buf.String(), "via sink")
	assert.Contains(t, buf.String(), `"key":"val"`)
}

// --- parseSince tests ---

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

// --- filterSources tests ---

func TestFilterSources_Empty_ReturnsAll(t *testing.T) {
	sources := filterSources("")
	assert.Equal(t, allLogSources, sources)
}

func TestFilterSources_SingleKey(t *testing.T) {
	sources := filterSources("cli")
	require.Len(t, sources, 1)
	assert.Equal(t, "cli", sources[0].key)
}

func TestFilterSources_MultipleKeys(t *testing.T) {
	sources := filterSources("cli,sandbox")
	require.Len(t, sources, 2)
	keys := []string{sources[0].key, sources[1].key}
	assert.ElementsMatch(t, []string{"cli", "sandbox"}, keys)
}

func TestFilterSources_WithSpaces(t *testing.T) {
	sources := filterSources("cli, hooks")
	require.Len(t, sources, 2)
}

func TestFilterSources_UnknownKeyIgnored(t *testing.T) {
	sources := filterSources("cli,nonexistent")
	require.Len(t, sources, 1)
	assert.Equal(t, "cli", sources[0].key)
}

// --- parseLogRecord edge-case tests ---

func TestParseLogRecord_RFC3339Timestamp(t *testing.T) {
	src := allLogSources[0]
	line := `{"ts":"2026-03-15T14:23:01Z","level":"info","event":"e","msg":"m"}`
	rec, ok := parseLogRecord(line, src)
	require.True(t, ok)
	assert.Equal(t, 2026, rec.ts.Year())
	assert.Equal(t, time.March, rec.ts.Month())
	assert.Equal(t, 15, rec.ts.Day())
}

func TestParseLogRecord_MissingTimestamp(t *testing.T) {
	src := allLogSources[0]
	before := time.Now().UTC()
	line := `{"level":"info","event":"e","msg":"no ts field"}`
	rec, ok := parseLogRecord(line, src)
	after := time.Now().UTC()
	require.True(t, ok)
	// Fallback: ts is set to time.Now() during parsing.
	assert.True(t, rec.ts.Equal(before) || rec.ts.After(before))
	assert.True(t, rec.ts.Equal(after) || rec.ts.Before(after))
}

func TestParseLogRecord_InvalidTimestamp(t *testing.T) {
	src := allLogSources[0]
	before := time.Now().UTC()
	line := `{"ts":"not-a-time","level":"info","event":"e","msg":"m"}`
	rec, ok := parseLogRecord(line, src)
	after := time.Now().UTC()
	require.True(t, ok)
	// Unparseable ts also falls back to time.Now().
	assert.True(t, rec.ts.Equal(before) || rec.ts.After(before))
	assert.True(t, rec.ts.Equal(after) || rec.ts.Before(after))
}

func TestParseLogRecord_NonStringExtraFields(t *testing.T) {
	src := allLogSources[0]
	line := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"e","msg":"m","count":42,"ok":true}`
	rec, ok := parseLogRecord(line, src)
	require.True(t, ok)

	extraMap := make(map[string]string)
	for _, kv := range rec.extra {
		extraMap[kv[0]] = kv[1]
	}
	assert.Equal(t, "42", extraMap["count"])
	assert.Equal(t, "true", extraMap["ok"])
}

func TestParseLogRecord_InvalidJSON(t *testing.T) {
	src := allLogSources[0]
	_, ok := parseLogRecord("not json at all", src)
	assert.False(t, ok)

	_, ok = parseLogRecord("{incomplete", src)
	assert.False(t, ok)
}

func TestParseLogRecord_PreservesRaw(t *testing.T) {
	src := allLogSources[0]
	line := `{"ts":"2026-03-15T14:23:01.000Z","level":"info","event":"e","msg":"m"}`
	rec, ok := parseLogRecord(line, src)
	require.True(t, ok)
	assert.Equal(t, line, rec.raw)
}

// --- formatRecord tests ---

func TestFormatRecord_ContainsExpectedParts(t *testing.T) {
	src := allLogSources[0] // cli
	rec := logRecord{
		ts:     time.Date(2026, 3, 15, 14, 23, 1, 0, time.UTC),
		level:  "info",
		event:  "sandbox.create",
		msg:    "creating sandbox",
		source: src,
	}
	out := formatRecord(rec, 0) // width=0 disables truncation
	assert.Contains(t, out, "INFO")
	assert.Contains(t, out, "sandbox.create")
	assert.Contains(t, out, "creating sandbox")
	assert.Contains(t, out, "14:23:01")
}

func TestFormatRecord_ExtraFieldsAppended(t *testing.T) {
	src := allLogSources[0]
	rec := logRecord{
		ts:     time.Now(),
		level:  "info",
		event:  "e",
		msg:    "msg",
		source: src,
		extra:  [][2]string{{"sandbox", "my-box"}, {"agent", "claude"}},
	}
	out := formatRecord(rec, 0)
	assert.Contains(t, out, "sandbox=my-box")
	assert.Contains(t, out, "agent=claude")
}

func TestFormatRecord_TruncatesAtWidth(t *testing.T) {
	src := allLogSources[0]
	rec := logRecord{
		ts:     time.Now(),
		level:  "info",
		event:  "e",
		msg:    strings.Repeat("x", 200),
		source: src,
	}
	out := formatRecord(rec, 80)
	assert.LessOrEqual(t, len(out), 80)
}

func TestFormatRecord_EventPaddedTo24(t *testing.T) {
	src := allLogSources[0]
	rec := logRecord{
		ts:     time.Now(),
		level:  "info",
		event:  "short",
		msg:    "msg",
		source: src,
	}
	out := formatRecord(rec, 0)
	// The event column is padded to 24 chars; "short" + 19 spaces should appear.
	assert.Contains(t, out, "short                  ")
}
