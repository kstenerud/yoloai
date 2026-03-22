package cli

// ABOUTME: Multi-sink slog logger for structured CLI logging.
// ABOUTME: Fans records to N independent sinks (stderr, cli.jsonl, bugreport temp file).

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

// multiSinkHandler fans log records to all registered sinks whose minLevel is satisfied.
type multiSinkHandler struct {
	mu    sync.Mutex
	sinks []sinkEntry
}

type sinkEntry struct {
	h        slog.Handler
	minLevel slog.Level
}

func (m *multiSinkHandler) addSink(h slog.Handler, minLevel slog.Level) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sinks = append(m.sinks, sinkEntry{h: h, minLevel: minLevel})
}

func (m *multiSinkHandler) Enabled(_ context.Context, level slog.Level) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sinks {
		if level >= s.minLevel {
			return true
		}
	}
	return false
}

func (m *multiSinkHandler) Handle(ctx context.Context, r slog.Record) error {
	m.mu.Lock()
	sinks := append([]sinkEntry(nil), m.sinks...)
	m.mu.Unlock()
	for _, s := range sinks {
		if r.Level >= s.minLevel {
			_ = s.h.Handle(ctx, r) //nolint:errcheck // best-effort log fan-out
		}
	}
	return nil
}

func (m *multiSinkHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := &multiSinkHandler{sinks: make([]sinkEntry, len(m.sinks))}
	for i, s := range m.sinks {
		n.sinks[i] = sinkEntry{h: s.h.WithAttrs(attrs), minLevel: s.minLevel}
	}
	return n
}

func (m *multiSinkHandler) WithGroup(name string) slog.Handler {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := &multiSinkHandler{sinks: make([]sinkEntry, len(m.sinks))}
	for i, s := range m.sinks {
		n.sinks[i] = sinkEntry{h: s.h.WithGroup(name), minLevel: s.minLevel}
	}
	return n
}

// Package-level logger state, initialised in PersistentPreRunE.
var globalHandler *multiSinkHandler

// liveLogBuf accumulates all JSONL log lines when --bugreport is active.
// Written via AddLogSink. Used to produce section 13 of the bug report.
var liveLogBuf bytes.Buffer

// bugReportFile is the open temp file for the current bug report, nil if not active.
var bugReportFile *os.File

// bugReportFinalName is the target filename (without .tmp) for the bug report.
var bugReportFinalName string

// bugReportType is "safe" or "unsafe", set when --bugreport is active.
var bugReportType string

// bugReportSandboxName is set by sandboxDispatch when --bugreport is active.
// The defer in Execute uses it to write sandbox sections 6-12.
var bugReportSandboxName string

// initLogger sets up the global multi-sink slog logger. Called from PersistentPreRunE
// before any subcommand logic runs. Default stderr level is WARN (lifecycle INFO events
// go to cli.jsonl only). -v raises to DEBUG; -q raises to ERROR. --debug affects
// cli.jsonl (added later per sandbox subcommand).
func initLogger(cmd *cobra.Command) {
	globalHandler = &multiSinkHandler{}

	verboseCount, _ := cmd.Flags().GetCount("verbose")
	quietCount, _ := cmd.Flags().GetCount("quiet")

	var stderrLevel slog.Level
	switch {
	case quietCount >= 1:
		stderrLevel = slog.LevelError
	case verboseCount >= 1:
		stderrLevel = slog.LevelDebug
	default:
		stderrLevel = slog.LevelWarn
	}

	globalHandler.addSink(newTextHandler(cmd.ErrOrStderr(), stderrLevel), stderrLevel)
	slog.SetDefault(slog.New(globalHandler))
}

// AddLogSink adds a JSONL-formatted sink to the global logger. Safe to call from
// RunE after initLogger has been called. Used by sandbox subcommands to register
// their cli.jsonl file.
func AddLogSink(w io.Writer, minLevel slog.Level) {
	if globalHandler == nil {
		return
	}
	globalHandler.addSink(newJSONLHandler(w, minLevel), minLevel)
}

// newJSONLHandler returns a slog.Handler that writes JSONL with yoloai field names:
// ts (RFC3339 milliseconds UTC), level (lowercase), msg, then any extra attrs.
func newJSONLHandler(w io.Writer, minLevel slog.Level) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: minLevel,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				return slog.String("ts", a.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z"))
			case slog.LevelKey:
				return slog.String("level", strings.ToLower(a.Value.String()))
			case slog.SourceKey:
				return slog.Attr{} // omit source file/line
			}
			return a
		},
	})
}

// openCLIJSONLSink opens logs/cli.jsonl for the named sandbox and registers it
// as a JSONL sink on the global logger. Returns a cleanup func (call with defer).
// No-op (returns empty func) if the sandbox logs directory doesn't exist yet.
func openCLIJSONLSink(name string, cmd *cobra.Command) func() {
	path := sandbox.CLIJSONLPath(name)
	f, err := fileutil.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // G304: path derived from trusted sandbox dir
	if err != nil {
		return func() {}
	}
	debug, _ := cmd.Flags().GetBool("debug")
	level := slog.LevelInfo
	if debug || bugReportFile != nil {
		level = slog.LevelDebug
	}
	AddLogSink(f, level)
	return func() { _ = f.Close() }
}

// newTextHandler returns a human-readable slog.Handler for stderr output.
// Omits timestamps (too noisy for interactive use).
func newTextHandler(w io.Writer, minLevel slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: minLevel,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey || a.Key == slog.SourceKey {
				return slog.Attr{} // omit
			}
			return a
		},
	})
}
