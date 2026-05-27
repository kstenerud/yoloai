package cliutil

import (
	"bytes"
	"context"
	"log/slog"
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
