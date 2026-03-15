package cli

// ABOUTME: `yoloai sandbox <name> bugreport` handler.
// ABOUTME: Forensic bug report tool: collects static diagnostic info from a named sandbox.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

// runSandboxBugReport produces a bug report for the named sandbox.
// Writes sections 1, 3-12 to the report file.
func runSandboxBugReport(cmd *cobra.Command, name string, reportType string) error {
	if reportType != "safe" && reportType != "unsafe" {
		return sandbox.NewUsageError("bugreport type must be safe or unsafe")
	}

	filename, err := bugReportFilename(time.Now())
	if err != nil {
		return fmt.Errorf("bugreport: %w", err)
	}

	f, err := os.OpenFile(filename+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) //nolint:gosec // G304: filename from bugReportFilename
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
	writeBugReportHeader(f, cliVersion, cliCommit, cliDate, reportType)

	// Section 3: System
	writeBugReportSystem(f)

	// Section 4: Backends
	writeBugReportBackends(cmd.Context(), f)

	// Section 5: Configuration
	writeBugReportConfig(f, reportType)

	// Sections 6-12: Sandbox-specific
	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		// Section 6: Sandbox detail
		writeBugReportSandboxDetail(ctx, f, rt, name, reportType)

		// Section 7: cli.jsonl
		writeBugReportJSONLFile(f, "logs/cli.jsonl", sandbox.CLIJSONLPath(name), reportType, nil)

		// Section 8: sandbox.jsonl (omit setup_cmd.* and network.allow in safe mode)
		var omitEvents []string
		if reportType == "safe" {
			omitEvents = []string{"setup_cmd.*", "network.allow"}
		}
		writeBugReportJSONLFile(f, "logs/sandbox.jsonl", sandbox.SandboxJSONLPath(name), reportType, omitEvents)

		// Section 9: monitor.jsonl (full in both modes)
		writeBugReportJSONLFile(f, "logs/monitor.jsonl", sandbox.MonitorJSONLPath(name), reportType, nil)

		// Section 10: agent-hooks.jsonl (full in both modes)
		writeBugReportJSONLFile(f, "logs/agent-hooks.jsonl", sandbox.HooksJSONLPath(name), reportType, nil)

		if reportType == "unsafe" {
			// Section 11: Agent output (unsafe only)
			writeBugReportAgentOutput(f, name)

			// Section 12: tmux screen capture (unsafe only)
			stateDir := sandbox.Dir(name)
			writeBugReportTmuxCapture(f, name, backend, stateDir)
		}

		return nil
	})
}

// writeBugReportSandboxDetail writes section 6: sandbox-specific detail.
func writeBugReportSandboxDetail(ctx context.Context, w io.Writer, rt runtime.Runtime, name, reportType string) {
	info, err := sandbox.InspectSandbox(ctx, rt, name)

	fmt.Fprintln(w, "<details>")                         //nolint:errcheck
	fmt.Fprintln(w, "<summary>Sandbox detail</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                      //nolint:errcheck

	if err != nil {
		fmt.Fprintf(w, "*(error inspecting sandbox: %s)*\n\n", err) //nolint:errcheck
	} else {
		meta := info.Meta
		fmt.Fprintf(w, "- **Name:** %s\n", meta.Name)       //nolint:errcheck
		fmt.Fprintf(w, "- **Status:** %s\n", info.Status)   //nolint:errcheck
		fmt.Fprintf(w, "- **Agent:** %s\n", meta.Agent)     //nolint:errcheck
		fmt.Fprintf(w, "- **Model:** %s\n", meta.Model)     //nolint:errcheck
		fmt.Fprintf(w, "- **Backend:** %s\n", meta.Backend) //nolint:errcheck
		fmt.Fprintln(w)                                     //nolint:errcheck
	}

	sandboxDir := sandbox.Dir(name)

	// environment.json
	writeJSONFileSection(w, "environment.json",
		fmt.Sprintf("%s/%s", sandboxDir, sandbox.EnvironmentFile),
		reportType, []string{"network_allow", "setup"})

	// agent-status.json (full contents in both modes)
	writePlainFileSection(w, "agent-status.json",
		fmt.Sprintf("%s/%s", sandboxDir, sandbox.AgentStatusFile))

	// runtime-config.json
	writeJSONFileSection(w, "runtime-config.json",
		fmt.Sprintf("%s/%s", sandboxDir, sandbox.RuntimeConfigFile),
		reportType, []string{"setup_commands", "allowed_domains"})

	// Container log
	writeContainerLog(w, name, resolveBackendForSandbox(name))

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

// writeContainerLog fetches container logs via the backend CLI.
func writeContainerLog(w io.Writer, name, backendName string) {
	fmt.Fprintln(w, "**Container log:**") //nolint:errcheck
	fmt.Fprintln(w)                       //nolint:errcheck

	containerName := sandbox.InstanceName(name)
	var args []string
	switch backendName {
	case "docker":
		args = []string{"docker", "logs", containerName}
	case "podman":
		args = []string{"podman", "logs", containerName}
	default:
		fmt.Fprintf(w, "*(not available for %s backend)*\n\n", backendName) //nolint:errcheck
		return
	}

	out, err := exec.Command(args[0], args[1:]...).CombinedOutput() //nolint:gosec // args[0] is trusted ("docker"/"podman")
	fmt.Fprintln(w, "```")                                          //nolint:errcheck
	if err != nil && len(out) == 0 {
		fmt.Fprintf(w, "*(error: %s)*\n", err) //nolint:errcheck
	} else {
		fmt.Fprintf(w, "%s", out) //nolint:errcheck
	}
	fmt.Fprintln(w, "```") //nolint:errcheck
	fmt.Fprintln(w)        //nolint:errcheck
}

// writeBugReportJSONLFile writes a JSONL file section with optional sanitization and event filtering.
func writeBugReportJSONLFile(w io.Writer, title, path, reportType string, omitEvents []string) {
	fmt.Fprintf(w, "<details>\n<summary>%s</summary>\n\n", title) //nolint:errcheck
	fmt.Fprintln(w, "```")                                        //nolint:errcheck

	data, err := sanitizeJSONLFile(path, reportType, omitEvents)
	if err != nil {
		fmt.Fprintf(w, "*(not found or unreadable)*\n") //nolint:errcheck
	} else {
		fmt.Fprintf(w, "%s", data) //nolint:errcheck
	}

	fmt.Fprintln(w, "```")        //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportAgentOutput writes section 11: ANSI-stripped agent output.
// Only included in unsafe reports.
func writeBugReportAgentOutput(w io.Writer, name string) {
	path := sandbox.AgentLogPath(name)
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		fmt.Fprintln(w, "<details>")                       //nolint:errcheck
		fmt.Fprintln(w, "<summary>Agent output</summary>") //nolint:errcheck
		fmt.Fprintln(w)                                    //nolint:errcheck
		fmt.Fprintln(w, "*(not found)*")                   //nolint:errcheck
		fmt.Fprintln(w, "</details>")                      //nolint:errcheck
		fmt.Fprintln(w)                                    //nolint:errcheck
		return
	}
	defer f.Close() //nolint:errcheck

	fmt.Fprintln(w, "<details>")                       //nolint:errcheck
	fmt.Fprintln(w, "<summary>Agent output</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                    //nolint:errcheck
	fmt.Fprintln(w, "```")                             //nolint:errcheck
	_ = stripANSI(w, f)
	fmt.Fprintln(w, "```")        //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportTmuxCapture writes section 12: tmux screen capture.
// Only included in unsafe reports. Silently omitted if sandbox is not running.
func writeBugReportTmuxCapture(w io.Writer, name, backendName, stateDir string) {
	var cmd *exec.Cmd
	if backendName == "seatbelt" {
		sock := fmt.Sprintf("%s/tmux/tmux.sock", stateDir)
		cmd = exec.Command("tmux", "-S", sock, "capture-pane", "-p", "-t", "main") //nolint:gosec
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
// omitEvents is a list of event patterns to skip (prefix match if ending in ".*").
func sanitizeJSONLFile(path, reportType string, omitEvents []string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path derived from trusted sandbox dir
	if err != nil {
		return nil, err
	}
	_ = reportType // sanitization always applied for consistency
	return sanitizeJSONLBytes(data, omitEvents), nil
}
