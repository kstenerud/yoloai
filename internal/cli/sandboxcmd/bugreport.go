package sandboxcmd

// ABOUTME: `yoloai sandbox <name> bugreport` handler.
// ABOUTME: Forensic bug report tool: collects static diagnostic info from a named sandbox.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/cli/bugreport"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/buildinfo"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// runSandboxBugReport produces a bug report for the named sandbox.
// Writes sections 1, 3-12 to the report file.
func runSandboxBugReport(cmd *cobra.Command, name string, reportType string) error {
	if reportType != "safe" && reportType != "unsafe" {
		return yoerrors.NewUsageError("bugreport type must be safe or unsafe")
	}

	filename, err := bugreport.Filename(time.Now())
	if err != nil {
		return fmt.Errorf("bugreport: %w", err)
	}

	f, err := fileutil.OpenFile(filename+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) //nolint:gosec // G304: filename from Filename
	if err != nil {
		return fmt.Errorf("bugreport: open temp file: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Rename(filename+".tmp", filename)
		if info, err := os.Stat(filename); err == nil && info.Size() > 65536 {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: report exceeds GitHub's issue body limit (65,536 characters).\n") //nolint:errcheck
			fmt.Fprintf(cmd.ErrOrStderr(), "Upload as a Gist instead: gh gist create %s\n", filename)                  //nolint:errcheck
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Bug report written: %s\n", filename) //nolint:errcheck
	}()

	// Section 1: Header
	bugreport.WriteHeader(f, buildinfo.Version, buildinfo.Commit, buildinfo.Date, reportType)

	// Sections 3-5: System, Backends, VM slots, Configuration — gathered as a
	// structured snapshot by the library and rendered here.
	bugreport.WriteDiagnostics(f, cliutil.System().Diagnostics(cmd.Context()), reportType)

	// Sections 6-12: Sandbox-specific
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		writeSandboxSections(ctx, f, c, name, reportType)
		return nil
	})
}

// writeSandboxSections writes sections 6-12 to w for the named sandbox.
// Called from both runSandboxBugReport (sandbox bugreport command) and
// WriteSandboxSectionsForFlag (--bugreport global flag on sandbox commands).
func writeSandboxSections(ctx context.Context, w io.Writer, c *yoloai.Client, name, reportType string) {
	// Section 6: Sandbox detail
	writeBugReportSandboxDetail(ctx, w, c, name, reportType)

	sandboxDir := cliutil.Layout().SandboxDir(name)
	sb, err := c.Sandbox(name)
	if err != nil {
		fmt.Fprintf(w, "(sandbox %q unavailable: %v)\n", name, err) //nolint:errcheck
		return
	}
	logs := sb.LogPaths()

	// Section 7: cli.jsonl
	writeBugReportJSONLFile(w, "logs/cli.jsonl", logs.CLI, reportType, nil)

	// Section 8: sandbox.jsonl (omit setup_cmd.* and network.allow in safe mode)
	var omitEvents []string
	if reportType == "safe" {
		omitEvents = []string{"setup_cmd.*", "network.allow"}
	}
	writeBugReportJSONLFile(w, "logs/sandbox.jsonl", logs.Sandbox, reportType, omitEvents)

	// Section 8.5: monitor detector tail (DF4) — last N detector.result
	// entries from monitor.jsonl, surfaced before the full stream so readers
	// see the decisive signal (wchan + connection-count) without scrolling
	// through the raw log.
	writeBugReportMonitorTail(w, logs.Monitor)

	// Section 9: monitor.jsonl (full in both modes)
	writeBugReportJSONLFile(w, "logs/monitor.jsonl", logs.Monitor, reportType, nil)

	// Section 10: agent-hooks.jsonl (full in both modes)
	writeBugReportJSONLFile(w, "logs/agent-hooks.jsonl", logs.Hooks, reportType, nil)

	// Section 10.5: network-diag.txt (DF9). Written only when the
	// containerd backend's waitForNetworkReady probe exhausts its
	// 30s budget — captures in-VM and host-side network state at the
	// moment of failure. Present only on probe-timeout; the section
	// header is suppressed when the file doesn't exist (typical).
	writePlainFileSection(w, "network-diag.txt", filepath.Join(sandboxDir, "network-diag.txt"))

	if reportType == "unsafe" {
		// Section 11: Agent output (unsafe only)
		writeBugReportAgentOutput(w, c, name)

		// Section 12: tmux screen capture (unsafe only).
		// Two variants: the historical host-side capture (works for
		// seatbelt where tmux runs on the host) and DF3's container-side
		// capture (works for docker/podman/containerd/tart where tmux
		// runs inside the sandbox). Both are best-effort and silently
		// skipped when not applicable; including both lets one bug
		// report cover every backend without the writer caring which
		// one is in use.
		writeBugReportTmuxCapture(w, sb)
		writeBugReportTerminalSnapshot(ctx, w, c, name)
	}
}

// WriteSandboxSectionsForFlag writes sections 6-12 for the --bugreport flag path.
// Called from the Execute defer when cliutil.BugReportSandboxName is set.
// Uses context.Background() since the command context may already be done.
func WriteSandboxSectionsForFlag(w io.Writer, name, reportType string) {
	backend := cliutil.ResolveBackendForSandbox(name)
	ctx := context.Background()
	l := cliutil.Layout()
	c, err := yoloai.NewWithOptions(ctx, yoloai.Options{
		DataDir: l.DataDir,
		HomeDir: l.HomeDir,
		Backend: yoloai.BackendName(backend),
		Input:   os.Stdin,
		Output:  io.Discard, // best-effort path; don't write to the in-progress bug report
		Env:     l.Env,
	})
	if err != nil {
		return
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	writeSandboxSections(ctx, w, c, name, reportType)
}

// writeBugReportSandboxDetail writes section 6: sandbox-specific detail.
func writeBugReportSandboxDetail(ctx context.Context, w io.Writer, c *yoloai.Client, name, reportType string) {
	sb, err := c.Sandbox(name)
	if err != nil {
		err = fmt.Errorf("sandbox handle: %w", err)
	}
	var info *yoloai.Info
	if err == nil {
		info, err = sb.Inspect(ctx)
	}

	fmt.Fprintln(w, "<details>")                         //nolint:errcheck
	fmt.Fprintln(w, "<summary>Sandbox detail</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                      //nolint:errcheck

	if err != nil {
		fmt.Fprintf(w, "*(error inspecting sandbox: %s)*\n\n", err) //nolint:errcheck
	} else {
		meta := info.Environment
		fmt.Fprintf(w, "- **Name:** %s\n", meta.Name)       //nolint:errcheck
		fmt.Fprintf(w, "- **Status:** %s\n", info.Status)   //nolint:errcheck
		fmt.Fprintf(w, "- **Agent:** %s\n", meta.Agent)     //nolint:errcheck
		fmt.Fprintf(w, "- **Model:** %s\n", meta.Model)     //nolint:errcheck
		fmt.Fprintf(w, "- **Backend:** %s\n", meta.Backend) //nolint:errcheck
		fmt.Fprintln(w)                                     //nolint:errcheck
	}

	sandboxDir := cliutil.Layout().SandboxDir(name)
	if sb != nil {
		// environment.json
		writeJSONFileSection(w, "environment.json",
			sb.EnvironmentPath(),
			reportType, []string{"network_allow", "setup"})

		// agent-status.json (full contents in both modes)
		writePlainFileSection(w, "agent-status.json",
			sb.LogPaths().AgentStatus)

		// runtime-config.json
		writeJSONFileSection(w, "runtime-config.json",
			sb.RuntimeConfigPath(),
			reportType, []string{"setup_commands", "allowed_domains"})

		// prompt.txt (unsafe only; omitted in safe mode — may contain sensitive task descriptions)
		if reportType == "unsafe" {
			writePlainFileSection(w, "prompt.txt",
				fmt.Sprintf("%s/prompt.txt", sandboxDir))
		}
	}

	// Container log
	writeContainerLog(ctx, w, c, name)

	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeJSONFileSection reads a JSON file, optionally removes sensitive keys in safe mode,
// and writes it as a code block.
func writeJSONFileSection(w io.Writer, label, path, reportType string, omitKeys []string) {
	fmt.Fprintf(w, "**%s:**\n\n", label) //nolint:errcheck
	fmt.Fprintln(w, "```json")           //nolint:errcheck
	data, err := os.ReadFile(path)       //nolint:gosec // G304: path is from trusted sandbox dir
	switch {
	case err != nil:
		fmt.Fprintln(w, "*(not found)*") //nolint:errcheck
	case reportType == "safe" && len(omitKeys) > 0:
		var obj map[string]json.RawMessage
		if jsonErr := json.Unmarshal(data, &obj); jsonErr == nil {
			for _, k := range omitKeys {
				delete(obj, k)
			}
			if sanitized, marshalErr := json.MarshalIndent(obj, "", "  "); marshalErr == nil {
				fmt.Fprintf(w, "%s\n", sanitized) //nolint:errcheck
			} else {
				fmt.Fprintf(w, "%s", data) //nolint:errcheck
			}
		} else {
			fmt.Fprintf(w, "%s", data) //nolint:errcheck
		}
	default:
		fmt.Fprintf(w, "%s", data) //nolint:errcheck
	}
	fmt.Fprintln(w, "```") //nolint:errcheck
	fmt.Fprintln(w)        //nolint:errcheck
}

// writePlainFileSection reads a file and writes it as a JSON code block.
func writePlainFileSection(w io.Writer, label, path string) {
	fmt.Fprintf(w, "**%s:**\n\n", label) //nolint:errcheck
	data, err := os.ReadFile(path)       //nolint:gosec // G304: path is from trusted sandbox dir
	if err != nil {
		fmt.Fprintln(w, "*(not found)*") //nolint:errcheck
		fmt.Fprintln(w)                  //nolint:errcheck
		return
	}
	fmt.Fprintln(w, "```json") //nolint:errcheck
	fmt.Fprintf(w, "%s", data) //nolint:errcheck
	fmt.Fprintln(w, "```")     //nolint:errcheck
	fmt.Fprintln(w)            //nolint:errcheck
}

// writeContainerLog fetches container logs via Client.ContainerLogs (which
// wraps the runtime's Logs method).
const containerLogTailLines = 1000

func writeContainerLog(ctx context.Context, w io.Writer, c *yoloai.Client, name string) {
	fmt.Fprintln(w, "**Container log:**") //nolint:errcheck
	fmt.Fprintln(w)                       //nolint:errcheck

	sb, sbErr := c.Sandbox(name)
	var logs string
	if sbErr == nil {
		logs = sb.Agent().ContainerLogs(ctx, containerLogTailLines)
	}
	fmt.Fprintln(w, "```") //nolint:errcheck
	if logs == "" {
		fmt.Fprintln(w, "*(no logs available)*") //nolint:errcheck
	} else {
		fmt.Fprintln(w, logs) //nolint:errcheck
	}
	fmt.Fprintln(w, "```") //nolint:errcheck
	fmt.Fprintln(w)        //nolint:errcheck
}

// terminalSnapshotScrollback is also used by the standalone CLI
// command in terminal_snapshot.go (same const value, declared once
// there). Keeping the constant in the same file as its CLI consumer
// avoids a cross-file dependency for what is essentially one tuning
// knob.

// monitorTailLines is the number of recent detector.result entries surfaced
// in the bug-report summary section. Same value as the smoke test's
// _MONITOR_TAIL_LINES — both produce the same shape of summary.
const monitorTailLines = 30

// writeBugReportMonitorTail extracts the last monitorTailLines detector.result
// entries from monitor.jsonl and writes them as a compact "Recent detector
// decisions" section. Surfaces the decisive failure signal (e.g. wchan's
// "do_epoll_wait + no connections -> idle") without requiring the reader to
// scroll through the full monitor.jsonl dump that follows. DF4.
func writeBugReportMonitorTail(w io.Writer, path string) {
	fmt.Fprintln(w, "**Recent detector decisions:**") //nolint:errcheck
	fmt.Fprintln(w)                                   //nolint:errcheck

	data, err := os.ReadFile(path) //nolint:gosec // G304: path from trusted sandbox dir
	if err != nil {
		fmt.Fprintln(w, "*(monitor.jsonl not found)*") //nolint:errcheck
		fmt.Fprintln(w)                                //nolint:errcheck
		return
	}

	type tailEntry struct {
		ts, msg string
	}
	var entries []tailEntry
	for _, raw := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var entry struct {
			TS    string `json:"ts"`
			Event string `json:"event"`
			Msg   string `json:"msg"`
		}
		if jsonErr := json.Unmarshal(line, &entry); jsonErr != nil {
			continue
		}
		if entry.Event != "detector.result" {
			continue
		}
		entries = append(entries, tailEntry{ts: entry.TS, msg: entry.Msg})
	}

	if len(entries) == 0 {
		fmt.Fprintln(w, "*(no detector.result entries in monitor.jsonl)*") //nolint:errcheck
		fmt.Fprintln(w)                                                    //nolint:errcheck
		return
	}

	tail := entries
	if len(tail) > monitorTailLines {
		tail = tail[len(tail)-monitorTailLines:]
	}

	fmt.Fprintf(w, "Last %d of %d entries (full stream in monitor.jsonl below).\n\n", len(tail), len(entries)) //nolint:errcheck
	fmt.Fprintln(w, "```")                                                                                     //nolint:errcheck
	for _, e := range tail {
		ts := e.ts
		// Drop trailing "Z" for readability; full ts still in monitor.jsonl.
		if len(ts) > 0 && ts[len(ts)-1] == 'Z' {
			ts = ts[:len(ts)-1]
		}
		fmt.Fprintf(w, "%s  %s\n", ts, e.msg) //nolint:errcheck
	}
	fmt.Fprintln(w, "```") //nolint:errcheck
	fmt.Fprintln(w)        //nolint:errcheck
}

// maxJSONLDumpLines caps how many lines of any one JSONL log are inlined into
// the report. The full logs live in the sandbox dir; the report only needs the
// recent tail to stay within GitHub's issue-body limit. monitor.jsonl is the
// main beneficiary — its decisive signal is already surfaced separately in the
// "Recent detector decisions" section.
const maxJSONLDumpLines = 500

// tailLines returns the last max lines of data and how many earlier lines were
// dropped. A trailing newline is preserved. data with <= max lines is returned
// unchanged with omitted == 0.
func tailLines(data []byte, max int) (tail []byte, omitted int) {
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	if len(lines) <= max {
		return data, 0
	}
	omitted = len(lines) - max
	return append(bytes.Join(lines[omitted:], []byte("\n")), '\n'), omitted
}

// writeBugReportJSONLFile writes a JSONL file section with optional sanitization and event filtering.
func writeBugReportJSONLFile(w io.Writer, title, path, reportType string, omitEvents []string) {
	fmt.Fprintf(w, "<details>\n<summary>%s</summary>\n\n", title) //nolint:errcheck
	fmt.Fprintln(w, "```")                                        //nolint:errcheck

	data, err := sanitizeJSONLFile(path, reportType, omitEvents)
	if err != nil {
		fmt.Fprintf(w, "*(not found or unreadable)*\n") //nolint:errcheck
	} else {
		data, omitted := tailLines(data, maxJSONLDumpLines)
		if omitted > 0 {
			fmt.Fprintf(w, "(showing last %d of %d lines — full file in the sandbox's logs dir)\n", maxJSONLDumpLines, maxJSONLDumpLines+omitted) //nolint:errcheck
		}
		fmt.Fprintf(w, "%s", data) //nolint:errcheck
	}

	fmt.Fprintln(w, "```")        //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportAgentOutput writes section 11: ANSI-stripped agent output.
// Only included in unsafe reports.
func writeBugReportAgentOutput(w io.Writer, c *yoloai.Client, name string) {
	output := ""
	if sb, err := c.Sandbox(name); err == nil {
		output, _ = sb.Agent().AgentLog(0)
	}
	if output == "" {
		fmt.Fprintln(w, "<details>")                       //nolint:errcheck
		fmt.Fprintln(w, "<summary>Agent output</summary>") //nolint:errcheck
		fmt.Fprintln(w)                                    //nolint:errcheck
		fmt.Fprintln(w, "*(not found)*")                   //nolint:errcheck
		fmt.Fprintln(w, "</details>")                      //nolint:errcheck
		fmt.Fprintln(w)                                    //nolint:errcheck
		return
	}

	fmt.Fprintln(w, "<details>")                       //nolint:errcheck
	fmt.Fprintln(w, "<summary>Agent output</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                    //nolint:errcheck
	fmt.Fprintln(w, "```")                             //nolint:errcheck
	_ = stripANSI(w, strings.NewReader(output))
	fmt.Fprintln(w, "```")        //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportTerminalSnapshot writes the DF3 container-side tmux
// capture as a bug-report section. Best-effort: silently omitted when
// the sandbox isn't running (typical for post-failure bug reports
// where the sandbox was already destroyed), or when the runtime
// doesn't support the capture (current scope: all primary backends).
// Unsafe-only because the captured pane may contain user prompts,
// API responses, or other sensitive content the safe report sanitizes.
func writeBugReportTerminalSnapshot(ctx context.Context, w io.Writer, c *yoloai.Client, name string) {
	sb, err := c.Sandbox(name)
	if err != nil {
		return
	}
	snap, err := sb.Agent().CaptureTerminal(ctx, terminalSnapshotScrollback)
	if err != nil {
		// Sandbox not running, runtime error, etc. — silently skip.
		return
	}
	if len(snap.Plain) == 0 && len(snap.ANSI) == 0 {
		return
	}

	fmt.Fprintln(w, "<details>")                                  //nolint:errcheck
	fmt.Fprintln(w, "<summary>Terminal snapshot (DF3)</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                               //nolint:errcheck

	if len(snap.Plain) > 0 {
		fmt.Fprintln(w, "**terminal-snapshot.txt (rendered):**") //nolint:errcheck
		fmt.Fprintln(w, "```")                                   //nolint:errcheck
		fmt.Fprintf(w, "%s", snap.Plain)                         //nolint:errcheck
		fmt.Fprintln(w, "```")                                   //nolint:errcheck
		fmt.Fprintln(w)                                          //nolint:errcheck
	}
	if len(snap.ANSI) > 0 {
		fmt.Fprintln(w, "**terminal-snapshot.ansi (with control sequences):**") //nolint:errcheck
		fmt.Fprintln(w, "```")                                                  //nolint:errcheck
		fmt.Fprintf(w, "%s", snap.ANSI)                                         //nolint:errcheck
		fmt.Fprintln(w, "```")                                                  //nolint:errcheck
		fmt.Fprintln(w)                                                         //nolint:errcheck
	}

	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportTmuxCapture writes section 12: tmux screen capture.
// Only included in unsafe reports. Silently omitted if sandbox is not running.
func writeBugReportTmuxCapture(w io.Writer, sb *yoloai.Sandbox) {
	tmuxSock := readTmuxSocketFromConfig(sb)
	var cmd *exec.Cmd
	if tmuxSock != "" {
		cmd = exec.Command("tmux", "-S", tmuxSock, "capture-pane", "-p", "-t", "main") //nolint:gosec // G204: tmuxSock is read from a yoloAI-owned config and validated as a path
	} else {
		cmd = exec.Command("tmux", "capture-pane", "-p", "-t", "main")
	}

	out, err := cmd.Output()
	if err != nil {
		return // silently omit if not running
	}

	fmt.Fprintln(w, "<details>")                              //nolint:errcheck
	fmt.Fprintln(w, "<summary>tmux screen capture</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                           //nolint:errcheck
	fmt.Fprintln(w, "```")                                    //nolint:errcheck
	fmt.Fprintf(w, "%s", out)                                 //nolint:errcheck
	fmt.Fprintln(w, "```")                                    //nolint:errcheck
	fmt.Fprintln(w)                                           //nolint:errcheck
	fmt.Fprintln(w, "</details>")                             //nolint:errcheck
	fmt.Fprintln(w)                                           //nolint:errcheck
}

// sanitizeJSONLFile reads a JSONL file, filters/sanitizes it, and returns the result.
// readTmuxSocketFromConfig reads the tmux_socket field from runtime-config.json
// for the named sandbox. Returns empty string if the file is missing or has no
// socket configured.
func readTmuxSocketFromConfig(sb *yoloai.Sandbox) string {
	if sb == nil {
		return ""
	}
	cfgPath := sb.RuntimeConfigPath()
	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path derived from trusted sandbox dir
	if err != nil {
		return ""
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return ""
	}
	sockRaw, ok := raw["tmux_socket"]
	if !ok {
		return ""
	}
	var sock string
	if err := json.Unmarshal(sockRaw, &sock); err != nil {
		return ""
	}
	return sock
}

// omitEvents is a list of event patterns to skip (prefix match if ending in ".*").
func sanitizeJSONLFile(path, reportType string, omitEvents []string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path derived from trusted sandbox dir
	if err != nil {
		return nil, err
	}
	// Secret redaction applies to safe reports only; an unsafe report is meant
	// to be a faithful, unredacted record (paths, container IDs, digests intact).
	return bugreport.SanitizeJSONLBytes(data, omitEvents, reportType == "safe"), nil
}
