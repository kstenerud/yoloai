// ABOUTME: Host-side activity-stream transport — merges the per-sandbox JSONL
// ABOUTME: log sources (cli/sandbox/monitor/hooks) into a time-ordered frame
// ABOUTME: stream, with optional follow (tail-poll) and terminal done-detection.
package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// LogFrame is one structured-log line as it sits on disk: the verbatim JSONL
// byte slice (Raw) plus the two fields the transport must understand to order
// and filter frames (Time, Level). Raw is canonical — any richer decomposition
// (event/msg/extra fields) is a presentation concern the consumer owns; the
// library never reshapes the payload.
type LogFrame struct {
	Source store.LogSource
	Time   time.Time
	Level  string
	Raw    []byte
}

// LogStreamOptions selects and filters the frames StreamLogs emits.
type LogStreamOptions struct {
	// Sources limits the streamed sources; empty means all four.
	Sources []store.LogSource
	// MinLevel drops frames below this level ("debug" < "info" < "warn" <
	// "error"). Empty means no level filter. An unknown value is a *UsageError.
	MinLevel string
	// Since drops frames older than this instant. Zero means no time filter.
	Since time.Time
	// Follow keeps the stream open after the backlog, tailing each source for
	// new lines until the agent reaches a terminal state or ctx is cancelled.
	Follow bool
}

// allLogSources is the canonical source order for backlog merging and the
// default when LogStreamOptions.Sources is empty.
var allLogSources = []store.LogSource{
	store.LogSourceCLI,
	store.LogSourceSandbox,
	store.LogSourceMonitor,
	store.LogSourceHooks,
}

// jsonlPathFor maps a log source to its on-disk JSONL path within sandboxDir.
func jsonlPathFor(sandboxDir string, src store.LogSource) string {
	switch src {
	case store.LogSourceCLI:
		return store.CLIJSONLPath(sandboxDir)
	case store.LogSourceSandbox:
		return store.SandboxJSONLPath(sandboxDir)
	case store.LogSourceMonitor:
		return store.MonitorJSONLPath(sandboxDir)
	case store.LogSourceHooks:
		return store.HooksJSONLPath(sandboxDir)
	default:
		return ""
	}
}

// sourcePath pairs a source with its resolved on-disk path.
type sourcePath struct {
	src  store.LogSource
	path string
}

// StreamLogs streams a sandbox's structured-log frames in time order. The
// backlog is read, merged, and emitted first; with opts.Follow the channel then
// stays open, tailing each source until the agent reaches a terminal state
// (read from agent-status.json) or ctx is cancelled. The returned channel is
// closed when the stream ends. A missing sandbox or invalid MinLevel is
// reported synchronously (the channel is nil on error).
func StreamLogs(ctx context.Context, layout config.Layout, name string, opts LogStreamOptions) (<-chan LogFrame, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	minLevel, err := parseMinLevel(opts.MinLevel)
	if err != nil {
		return nil, err
	}

	srcSet := opts.Sources
	if len(srcSet) == 0 {
		srcSet = allLogSources
	}
	srcs := make([]sourcePath, 0, len(srcSet))
	for _, s := range srcSet {
		if p := jsonlPathFor(sandboxDir, s); p != "" {
			srcs = append(srcs, sourcePath{src: s, path: p})
		}
	}

	out := make(chan LogFrame, 64)
	go func() {
		defer close(out)

		backlog := readBacklog(srcs, minLevel, opts.Since)
		for _, f := range backlog {
			select {
			case out <- f:
			case <-ctx.Done():
				return
			}
		}

		if opts.Follow {
			followLogs(ctx, sandboxDir, srcs, minLevel, opts.Since, out)
		}
	}()
	return out, nil
}

// readBacklog reads every source fully, applies the level/since filters, and
// returns the frames merged into a single time-ordered slice.
func readBacklog(srcs []sourcePath, minLevel int, since time.Time) []LogFrame {
	var all []LogFrame
	for _, sp := range srcs {
		all = append(all, readLogFrames(sp, minLevel, since)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Time.Before(all[j].Time)
	})
	return all
}

// readLogFrames reads all matching frames from one source's file. A missing
// file is not an error — it yields no frames.
func readLogFrames(sp sourcePath, minLevel int, since time.Time) []LogFrame {
	f, err := os.Open(sp.path) //nolint:gosec // G304: path is a store.*JSONLPath — yoloAI-owned
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // read-only file

	var frames []LogFrame
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		if frame, ok := frameFromLine(scanner.Bytes(), sp.src, minLevel, since); ok {
			frames = append(frames, frame)
		}
	}
	return frames
}

// frameFromLine parses one JSONL line into a LogFrame, applying the level and
// since filters. Returns false for blank lines, unparseable JSON, or frames the
// filters exclude. Raw is copied (the scanner reuses its buffer).
func frameFromLine(line []byte, src store.LogSource, minLevel int, since time.Time) (LogFrame, bool) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return LogFrame{}, false
	}
	var hdr struct {
		Ts    string `json:"ts"`
		Level string `json:"level"`
	}
	if err := json.Unmarshal([]byte(trimmed), &hdr); err != nil {
		return LogFrame{}, false
	}
	if levelOrder(hdr.Level) < minLevel {
		return LogFrame{}, false
	}
	ts := parseFrameTimestamp(hdr.Ts)
	if !since.IsZero() && ts.Before(since) {
		return LogFrame{}, false
	}
	return LogFrame{
		Source: src,
		Time:   ts,
		Level:  hdr.Level,
		Raw:    []byte(trimmed),
	}, true
}

// parseFrameTimestamp parses the "ts" field, accepting both our RFC3339-millis
// format and full RFC3339, and falling back to now for missing/unparseable
// values (so a frame is never silently dropped on a clock-format quirk).
func parseFrameTimestamp(ts string) time.Time {
	if ts != "" {
		for _, layout := range []string{"2006-01-02T15:04:05.000Z", time.RFC3339} {
			if t, err := time.Parse(layout, ts); err == nil {
				return t
			}
		}
	}
	return time.Now().UTC()
}

// levelOrder maps a level name to its numeric severity for filtering. Unknown
// levels are treated as info. Mirrors the producer's level vocabulary.
func levelOrder(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn", "warning":
		return 2
	case "error":
		return 3
	default:
		return 1
	}
}

// parseMinLevel resolves a MinLevel string to its numeric order. Empty means no
// filter (order 0, the most permissive); an unknown name is a *UsageError so
// the library — not each consumer — owns the level vocabulary.
func parseMinLevel(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	switch strings.ToLower(s) {
	case "debug", "info", "warn", "warning", "error":
		return levelOrder(s), nil
	default:
		return 0, yoerrors.NewUsageError("unknown level %q: must be debug, info, warn, or error", s)
	}
}

// followLogs tails each source concurrently after the backlog, forwarding new
// frames to out until the agent reaches a terminal state or ctx is cancelled.
func followLogs(ctx context.Context, sandboxDir string, srcs []sourcePath, minLevel int, since time.Time, out chan<- LogFrame) {
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()

	frames := startTailers(tctx, srcs, minLevel, since)

	doneCheck := time.NewTicker(2 * time.Second)
	defer doneCheck.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-frames:
			if !ok || !forwardFrame(ctx, out, f) {
				return
			}
		case <-doneCheck.C:
			if sandboxIsDone(sandboxDir) {
				cancel() // stop tailers; drain whatever they already buffered
				drainFrames(ctx, frames, out)
				return
			}
		}
	}
}

// startTailers launches one tail goroutine per source and returns a fan-in
// channel that is closed once every tailer has stopped (tctx cancelled).
func startTailers(tctx context.Context, srcs []sourcePath, minLevel int, since time.Time) <-chan LogFrame {
	frames := make(chan LogFrame, 64)
	var wg sync.WaitGroup
	for _, sp := range srcs {
		wg.Add(1)
		go func(sp sourcePath) {
			defer wg.Done()
			tailSource(tctx, sp, initialOffset(sp.path), minLevel, since, frames)
		}(sp)
	}
	go func() { wg.Wait(); close(frames) }()
	return frames
}

// forwardFrame sends f to out, reporting false if ctx was cancelled first.
func forwardFrame(ctx context.Context, out chan<- LogFrame, f LogFrame) bool {
	select {
	case out <- f:
		return true
	case <-ctx.Done():
		return false
	}
}

// drainFrames forwards whatever the (now-closing) tailers already buffered.
func drainFrames(ctx context.Context, frames <-chan LogFrame, out chan<- LogFrame) {
	for f := range frames {
		if !forwardFrame(ctx, out, f) {
			return
		}
	}
}

// initialOffset returns the byte length of path (where a tail begins so the
// backlog already emitted isn't re-sent). A missing file starts at 0.
func initialOffset(path string) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.Size()
	}
	return 0
}

// tailSource polls one source file every 500ms, forwarding new frames to ch
// until ctx is cancelled.
func tailSource(ctx context.Context, sp sourcePath, offset int64, minLevel int, since time.Time, ch chan<- LogFrame) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		offset = pollSource(ctx, sp, offset, minLevel, since, ch)
	}
}

// pollSource reads new lines from offset, forwarding matching frames to ch, and
// returns the offset past the last consumed line.
func pollSource(ctx context.Context, sp sourcePath, offset int64, minLevel int, since time.Time, ch chan<- LogFrame) int64 {
	f, err := os.Open(sp.path) //nolint:gosec // G304: path is a store.*JSONLPath — yoloAI-owned
	if err != nil {
		return offset
	}
	defer f.Close() //nolint:errcheck // read-only file
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		raw := scanner.Bytes()
		offset += int64(len(raw) + 1) // +1 for the consumed newline
		frame, ok := frameFromLine(raw, sp.src, minLevel, since)
		if !ok {
			continue
		}
		select {
		case ch <- frame:
		case <-ctx.Done():
			return offset
		}
	}
	return offset
}

// sandboxIsDone reports whether agent-status.json shows a terminal agent state.
// A missing/unreadable file is treated as not-done (keep following).
func sandboxIsDone(sandboxDir string) bool {
	data, err := os.ReadFile(store.AgentStatusFilePath(sandboxDir)) //nolint:gosec // G304: yoloAI-owned agent-status.json
	if err != nil {
		return false
	}
	var status struct {
		Status   string `json:"status"`
		ExitCode *int   `json:"exit_code"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return false
	}
	return status.Status == "done" || status.Status == "failed" ||
		(status.Status == "idle" && status.ExitCode != nil)
}
