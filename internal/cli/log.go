package cli

// ABOUTME: Sandbox log display: pretty-prints structured JSONL from all four log
// ABOUTME: sources (cli, sandbox, monitor, hooks), with optional follow mode,
// ABOUTME: level/source/since filtering, raw JSONL output, and agent terminal output.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

// logSource describes one JSONL log source.
type logSource struct {
	key   string
	label string // 7 chars, right-padded
	path  func(name string) string
}

var allLogSources = []logSource{
	{"cli", "cli    ", sandbox.CLIJSONLPath},
	{"sandbox", "sandbox", sandbox.SandboxJSONLPath},
	{"monitor", "monitor", sandbox.MonitorJSONLPath},
	{"hooks", "hooks  ", sandbox.HooksJSONLPath},
}

// logRecord is a parsed JSONL log entry.
type logRecord struct {
	ts     time.Time
	level  string
	event  string
	msg    string
	source logSource
	extra  [][2]string // ordered key=val pairs (excluding ts, level, event, msg)
	raw    string      // original line for --raw mode
}

// parseLogRecord parses one JSONL line. Returns false if not valid JSONL.
func parseLogRecord(line string, src logSource) (logRecord, bool) {
	// Use a raw map to extract known fields then collect the rest.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return logRecord{}, false
	}

	rec := logRecord{source: src, raw: line}

	if v, ok := raw["ts"]; ok {
		var ts string
		if json.Unmarshal(v, &ts) == nil {
			// Accept both RFC3339 milliseconds (our format) and full RFC3339.
			for _, layout := range []string{"2006-01-02T15:04:05.000Z", time.RFC3339} {
				if t, err := time.Parse(layout, ts); err == nil {
					rec.ts = t
					break
				}
			}
		}
	}
	if rec.ts.IsZero() {
		rec.ts = time.Now().UTC()
	}

	jsonString := func(key string) string {
		if v, ok := raw[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				return s
			}
		}
		return ""
	}

	rec.level = jsonString("level")
	rec.event = jsonString("event")
	rec.msg = jsonString("msg")

	skip := map[string]bool{"ts": true, "level": true, "event": true, "msg": true}
	for k, v := range raw {
		if skip[k] {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			rec.extra = append(rec.extra, [2]string{k, s})
		} else {
			// non-string: emit as JSON token
			rec.extra = append(rec.extra, [2]string{k, string(v)})
		}
	}

	return rec, true
}

// levelOrder returns numeric order for log level comparisons.
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
		return 1 // treat unknown as info
	}
}

// levelCode returns a 4-char uppercase level code.
func levelCode(level string) string {
	switch strings.ToLower(level) {
	case "debug":
		return "DBUG"
	case "info":
		return "INFO"
	case "warn", "warning":
		return "WARN"
	case "error":
		return "ERRO"
	default:
		code := strings.ToUpper(level)
		if len(code) >= 4 {
			return code[:4]
		}
		return fmt.Sprintf("%-4s", code)
	}
}

// parseLogLevel parses a level name into its numeric order.
func parseLogLevel(s string) (int, error) {
	switch strings.ToLower(s) {
	case "debug", "info", "warn", "warning", "error":
		return levelOrder(s), nil
	default:
		return 0, sandbox.NewUsageError("unknown level %q: must be debug, info, warn, or error", s)
	}
}

// parseSince parses a --since value into a UTC time.
// Accepts Go durations ("5m") or local time strings ("14:20:00").
func parseSince(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().UTC().Add(-d), nil
	}
	now := time.Now()
	for _, layout := range []string{"15:04:05", "15:04"} {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), t.Second(), 0, time.Local).UTC(), nil
		}
	}
	return time.Time{}, sandbox.NewUsageError("unrecognized format: use a duration (e.g. 5m) or local time (e.g. 14:20:00)")
}

// filterSources returns active sources based on the --source flag.
// If sourceFlag is empty, all sources are returned.
func filterSources(sourceFlag string) []logSource {
	if sourceFlag == "" {
		return allLogSources
	}
	keySet := make(map[string]bool)
	for _, k := range strings.Split(sourceFlag, ",") {
		keySet[strings.TrimSpace(k)] = true
	}
	var result []logSource
	for _, src := range allLogSources {
		if keySet[src.key] {
			result = append(result, src)
		}
	}
	return result
}

// terminalWidth returns the output width from $COLUMNS or os.Stdout, falling back to 120.
func terminalWidth() int {
	if s := os.Getenv("COLUMNS"); s != "" {
		var w int
		if _, err := fmt.Sscanf(s, "%d", &w); err == nil && w > 0 {
			return w
		}
	}
	return 120
}

// formatRecord formats a logRecord as a single pretty-printed line.
//
// Format: HH:MM:SS src     LEVL  event-name               message  key=val...
func formatRecord(rec logRecord, width int) string {
	local := rec.ts.Local()
	timeStr := local.Format("15:04:05")
	lvl := levelCode(rec.level)

	event := rec.event
	if len(event) > 24 {
		event = event[:24]
	} else {
		event = fmt.Sprintf("%-24s", event)
	}

	prefix := fmt.Sprintf("%s %s %s  %s ", timeStr, rec.source.label, lvl, event)
	rest := rec.msg
	for _, kv := range rec.extra {
		rest += "  " + kv[0] + "=" + kv[1]
	}

	if width > 0 && len(prefix)+len(rest) > width {
		if remaining := width - len(prefix); remaining > 0 {
			rest = rest[:remaining]
		} else {
			rest = ""
		}
	}

	return prefix + rest
}

// readLogFile reads all logRecords from a JSONL file, applying level and time filters.
func readLogFile(path string, src logSource, minLevel int, sinceTime time.Time) []logRecord {
	f, err := os.Open(path) //nolint:gosec // G304: path derived from trusted sandbox dir
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck

	var records []logRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		rec, ok := parseLogRecord(line, src)
		if !ok {
			continue
		}
		if levelOrder(rec.level) < minLevel {
			continue
		}
		if !sinceTime.IsZero() && rec.ts.Before(sinceTime) {
			continue
		}
		records = append(records, rec)
	}
	return records
}

// runLogStatic reads all JSONL sources, merge-sorts by timestamp, and emits.
func runLogStatic(cmd *cobra.Command, name string, sources []logSource, minLevel int, sinceTime time.Time, rawMode bool) error {
	var all []logRecord
	for _, src := range sources {
		recs := readLogFile(src.path(name), src, minLevel, sinceTime)
		all = append(all, recs...)
	}

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].ts.Before(all[j].ts)
	})

	if len(all) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No log entries found.") //nolint:errcheck
		return nil
	}

	width := terminalWidth()
	out := cmd.OutOrStdout()
	for _, rec := range all {
		if rawMode {
			fmt.Fprintln(out, rec.raw) //nolint:errcheck
		} else {
			fmt.Fprintln(out, formatRecord(rec, width)) //nolint:errcheck
		}
	}
	return nil
}

// tailFile polls a JSONL file for new lines, sending parsed records to ch.
// Signals done by sending a tailEntry{done: true} when ctx is cancelled.
func tailFile(
	cmd *cobra.Command,
	src logSource,
	offset int64,
	minLevel int,
	sinceTime time.Time,
	rawMode bool,
	ch chan<- string,
	done <-chan struct{},
) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}

		f, err := os.Open(src.path("")) //nolint:gosec // path is trusted
		if err != nil {
			continue
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		aborted := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			rec, ok := parseLogRecord(line, src)
			if !ok {
				continue
			}
			if levelOrder(rec.level) < minLevel {
				continue
			}
			if !sinceTime.IsZero() && rec.ts.Before(sinceTime) {
				continue
			}
			offset += int64(len(scanner.Bytes()) + 1) // +1 for newline
			var toSend string
			if rawMode {
				toSend = line
			} else {
				toSend = formatRecord(rec, terminalWidth())
			}
			select {
			case ch <- toSend:
			case <-done:
				aborted = true
			}
			if aborted {
				break
			}
		}
		_ = f.Close()
		if aborted {
			return
		}
	}
}

// sandboxIsDone returns true if the sandbox's agent-status.json shows the agent has exited.
func sandboxIsDone(name string) bool {
	statusPath := sandbox.AgentStatusFilePath(name)
	data, err := os.ReadFile(statusPath) //nolint:gosec
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

// runLogFollow tails all sources concurrently and emits as lines arrive.
func runLogFollow(cmd *cobra.Command, name string, sources []logSource, minLevel int, sinceTime time.Time, rawMode bool) error {
	// First emit static backlog
	if err := runLogStatic(cmd, name, sources, minLevel, sinceTime, rawMode); err != nil {
		return err
	}

	ch := make(chan string, 64)
	done := make(chan struct{})

	for _, src := range sources {
		// Calculate initial offset (end of file)
		var offset int64
		if f, err := os.Open(src.path(name)); err == nil { //nolint:gosec
			offset, _ = f.Seek(0, io.SeekEnd)
			_ = f.Close()
		}
		srcCopy2 := logSource{
			key:   src.key,
			label: src.label,
			path:  func(_ string) string { return src.path(name) },
		}
		go tailFile(cmd, srcCopy2, offset, minLevel, sinceTime, rawMode, ch, done)
	}

	out := cmd.OutOrStdout()
	exitCheck := time.NewTicker(2 * time.Second)
	defer exitCheck.Stop()

	for {
		select {
		case line := <-ch:
			fmt.Fprintln(out, line) //nolint:errcheck
		case <-exitCheck.C:
			if sandboxIsDone(name) {
				// Drain remaining buffered lines
				for {
					select {
					case line := <-ch:
						fmt.Fprintln(out, line) //nolint:errcheck
					default:
						close(done)
						return nil
					}
				}
			}
		}
	}
}

// runLogAgent shows the raw agent terminal output (logs/agent.log).
func runLogAgent(cmd *cobra.Command, name string, rawMode bool) error {
	logPath := sandbox.AgentLogPath(name)
	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(cmd.OutOrStdout(), "No agent output yet") //nolint:errcheck
			return nil
		}
		return fmt.Errorf("open agent log: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if rawMode {
		_, err = io.Copy(cmd.OutOrStdout(), f)
		return err
	}
	return stripANSI(cmd.OutOrStdout(), f)
}

// runLog is the shared implementation for `sandbox log` and the `log` alias.
func runLog(cmd *cobra.Command, args []string) error {
	name, _, err := resolveName(cmd, args)
	if err != nil {
		return err
	}

	agentFlag, _ := cmd.Flags().GetBool("agent")
	agentRawFlag, _ := cmd.Flags().GetBool("agent-raw")
	rawFlag, _ := cmd.Flags().GetBool("raw")
	sourceFlag, _ := cmd.Flags().GetString("source")
	levelFlag, _ := cmd.Flags().GetString("level")
	sinceFlag, _ := cmd.Flags().GetString("since")
	followFlag, _ := cmd.Flags().GetBool("follow")

	// Agent output modes are mutually exclusive with structured log options.
	if agentFlag || agentRawFlag {
		return runLogAgent(cmd, name, agentRawFlag)
	}

	if levelFlag == "" {
		levelFlag = "info"
	}
	minLevel, err := parseLogLevel(levelFlag)
	if err != nil {
		return err
	}

	var sinceTime time.Time
	if sinceFlag != "" {
		sinceTime, err = parseSince(sinceFlag)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
	}

	activeSources := filterSources(sourceFlag)

	if followFlag {
		return runLogFollow(cmd, name, activeSources, minLevel, sinceTime, rawFlag)
	}
	return runLogStatic(cmd, name, activeSources, minLevel, sinceTime, rawFlag)
}
