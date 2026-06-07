package sandboxcmd

// ABOUTME: Sandbox log display: pretty-prints the structured JSONL frames the
// ABOUTME: library's activity stream (System.Logs) delivers, with optional
// ABOUTME: follow, level/source/since filtering, raw passthrough, and agent output.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// sourceLabels gives each log source its 7-char, right-padded display label.
var sourceLabels = map[yoloai.LogSource]string{
	yoloai.LogSourceCLI:     "cli    ",
	yoloai.LogSourceSandbox: "sandbox",
	yoloai.LogSourceMonitor: "monitor",
	yoloai.LogSourceHooks:   "hooks  ",
}

// logRecord is a parsed JSONL log entry, decomposed for pretty-printing. The
// library hands us the raw line; turning it into display fields is CLI work.
type logRecord struct {
	timestamp   time.Time
	level       string
	event       string
	msg         string
	sourceLabel string
	extra       [][2]string // ordered key=val pairs (excluding ts, level, event, msg)
}

// parseLogRecord parses one JSONL line for display. Returns false if not valid
// JSONL (the caller falls back to printing the raw line).
func parseLogRecord(line string, sourceLabel string) (logRecord, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return logRecord{}, false
	}

	rec := logRecord{sourceLabel: sourceLabel}
	rec.timestamp = parseLogTimestamp(raw)
	rec.level = rawJSONString(raw, "level")
	rec.event = rawJSONString(raw, "event")
	rec.msg = rawJSONString(raw, "msg")
	rec.extra = collectLogExtras(raw)

	return rec, true
}

// parseLogTimestamp extracts and parses the "ts" field, falling back to now.
func parseLogTimestamp(raw map[string]json.RawMessage) time.Time {
	if v, ok := raw["ts"]; ok {
		var ts string
		if json.Unmarshal(v, &ts) == nil {
			// Accept both RFC3339 milliseconds (our format) and full RFC3339.
			for _, layout := range []string{"2006-01-02T15:04:05.000Z", time.RFC3339} {
				if t, err := time.Parse(layout, ts); err == nil {
					return t
				}
			}
		}
	}
	return time.Now().UTC()
}

// rawJSONString extracts a string value from a raw JSON map by key.
func rawJSONString(raw map[string]json.RawMessage, key string) string {
	if v, ok := raw[key]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	return ""
}

// collectLogExtras collects all fields except the standard log fields as ordered pairs.
func collectLogExtras(raw map[string]json.RawMessage) [][2]string {
	skip := map[string]bool{"ts": true, "level": true, "event": true, "msg": true}
	var extra [][2]string
	for k, v := range raw {
		if skip[k] {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			extra = append(extra, [2]string{k, s})
		} else {
			// non-string: emit as JSON token
			extra = append(extra, [2]string{k, string(v)})
		}
	}
	return extra
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

// parseSince parses a --since value into a UTC time.
// Accepts Go durations ("5m") or local time strings ("14:20:00").
func parseSince(s string) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().UTC().Add(-d), nil
	}
	now := time.Now()
	for _, layout := range []string{"15:04:05", "15:04"} {
		t, err := time.ParseInLocation(layout, s, time.Local) //nolint:forbidigo // §12: intentionally parse the user-typed log filter time in their local tz
		if err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), t.Second(), 0, time.Local).UTC(), nil //nolint:forbidigo // §12: same local-tz interpretation of user input, normalized to UTC
		}
	}
	return time.Time{}, yoerrors.NewUsageError("unrecognized format: use a duration (e.g. 5m) or local time (e.g. 14:20:00)")
}

// parseSourceFlag turns the --source value into a LogSource list. Empty means
// all sources (nil). Unknown keys are silently dropped, matching prior behavior.
func parseSourceFlag(sourceFlag string) []yoloai.LogSource {
	if sourceFlag == "" {
		return nil
	}
	var result []yoloai.LogSource
	for k := range strings.SplitSeq(sourceFlag, ",") {
		key := strings.TrimSpace(k)
		if _, ok := sourceLabels[yoloai.LogSource(key)]; ok {
			result = append(result, yoloai.LogSource(key))
		}
	}
	return result
}

// terminalWidth returns the output width from $COLUMNS or os.Stdout, falling back to 120.
func terminalWidth() int {
	if s := os.Getenv("COLUMNS"); s != "" { //nolint:forbidigo // §12: CLI terminal-width detection for log output formatting
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
	local := rec.timestamp.Local()
	timeStr := local.Format("15:04:05")
	lvl := levelCode(rec.level)

	event := rec.event
	if len(event) > 24 {
		event = event[:24]
	} else {
		event = fmt.Sprintf("%-24s", event)
	}

	prefix := fmt.Sprintf("%s %s %s  %s ", timeStr, rec.sourceLabel, lvl, event)
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

// runLogStructured consumes the library activity stream and renders it.
func runLogStructured(cmd *cobra.Command, name string, opts yoloai.AgentLogsOptions, rawMode bool) error {
	c, err := cliutil.Client(cmd)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	sb, err := c.Sandbox(name)
	if err != nil {
		return err
	}
	events, err := sb.Agent().Logs(cmd.Context(), opts)
	if err != nil {
		return err
	}

	width := terminalWidth()
	out := cmd.OutOrStdout()
	printed := 0
	for ev := range events {
		printed++
		if rawMode {
			fmt.Fprintln(out, string(ev.Raw)) //nolint:errcheck
			continue
		}
		rec, ok := parseLogRecord(string(ev.Raw), sourceLabels[ev.Source])
		if !ok {
			fmt.Fprintln(out, string(ev.Raw)) //nolint:errcheck
			continue
		}
		fmt.Fprintln(out, formatRecord(rec, width)) //nolint:errcheck
	}

	if printed == 0 && !opts.Follow {
		fmt.Fprintln(out, "No log entries found.") //nolint:errcheck
	}
	return nil
}

// runLogAgent shows the raw agent terminal output (logs/agent.log).
func runLogAgent(cmd *cobra.Command, name string, rawMode bool) error {
	c, err := cliutil.Client(cmd)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	sb, err := c.Sandbox(name)
	if err != nil {
		return err
	}
	output, err := sb.Agent().TerminalLog(0)
	if err != nil {
		return err
	}
	if output == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "No agent output yet") //nolint:errcheck
		return nil
	}

	if rawMode {
		_, err = io.WriteString(cmd.OutOrStdout(), output)
		return err
	}
	return stripANSI(cmd.OutOrStdout(), strings.NewReader(output))
}

// runLog is the shared implementation for `sandbox log` and the `log` alias.
func runLog(cmd *cobra.Command, args []string) error {
	name, _, err := cliutil.ResolveName(cmd, args)
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

	var sinceTime time.Time
	if sinceFlag != "" {
		sinceTime, err = parseSince(sinceFlag)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
	}

	opts := yoloai.AgentLogsOptions{
		Sources:  parseSourceFlag(sourceFlag),
		MinLevel: levelFlag,
		Since:    sinceTime,
		Follow:   followFlag,
	}
	return runLogStructured(cmd, name, opts, rawFlag)
}
