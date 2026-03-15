package cli

// ABOUTME: Bug report section writers and sanitization helpers.
// ABOUTME: Shared by --bugreport flag (flight recorder) and sandbox bugreport command.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/sandbox"
)

// bugReportFilename generates the output filename for a bug report.
// Returns an error if a file with the same name already exists.
func bugReportFilename(t time.Time) (string, error) {
	name := fmt.Sprintf("yoloai-bugreport-%s.md",
		t.UTC().Format("20060102-150405.000"))
	if _, err := os.Stat(name); err == nil {
		return "", fmt.Errorf("file already exists: %s", name)
	}
	return name, nil
}

// writeBugReportHeader writes section 1: report header.
func writeBugReportHeader(w io.Writer, version, commit, date, reportType string) {
	now := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(w, "## yoloai Bug Report — %s\n\n", now) //nolint:errcheck

	switch reportType {
	case "unsafe":
		fmt.Fprintln(w, "> ⛔ UNSAFE REPORT — unsanitized, contains all logs and agent output.") //nolint:errcheck
		fmt.Fprintln(w, "> Do not share publicly.")                                             //nolint:errcheck
	default:
		fmt.Fprintln(w, "> ⚠️ Review before sharing: this report may contain proprietary code,") //nolint:errcheck
		fmt.Fprintln(w, "> task descriptions, file paths, and internal configuration.")          //nolint:errcheck
	}
	fmt.Fprintln(w) //nolint:errcheck

	fmt.Fprintf(w, "**Version:** %s (%s, %s)\n", version, commit, date) //nolint:errcheck
	fmt.Fprintf(w, "**Type:** %s\n", reportType)                        //nolint:errcheck
}

// writeBugReportCommandInvocation writes section 2: the full command invocation.
// In safe mode, --prompt / -p values are redacted.
func writeBugReportCommandInvocation(w io.Writer, reportType string) {
	args := os.Args
	if reportType == "safe" {
		args = redactPromptArgs(args)
	}
	cmd := strings.Join(args, " ")
	fmt.Fprintf(w, "**Command:** `%s`\n\n", cmd) //nolint:errcheck
}

// redactPromptArgs redacts the values of --prompt / -p flags.
func redactPromptArgs(args []string) []string {
	result := make([]string, len(args))
	copy(result, args)
	for i, arg := range result {
		if (arg == "--prompt" || arg == "-p") && i+1 < len(result) {
			result[i+1] = "[REDACTED]"
		}
		if strings.HasPrefix(arg, "--prompt=") {
			result[i] = "--prompt=[REDACTED]"
		}
	}
	return result
}

// writeBugReportSystem writes section 3: system information.
func writeBugReportSystem(w io.Writer) {
	fmt.Fprintln(w, "<details>")                 //nolint:errcheck
	fmt.Fprintln(w, "<summary>System</summary>") //nolint:errcheck
	fmt.Fprintln(w)                              //nolint:errcheck

	fmt.Fprintf(w, "- **OS/Arch:** %s/%s\n", runtime.GOOS, runtime.GOARCH) //nolint:errcheck

	// Kernel string
	uname, err := exec.Command("uname", "-a").Output()
	if err == nil {
		fmt.Fprintf(w, "- **Kernel:** %s\n", strings.TrimSpace(string(uname))) //nolint:errcheck
	}

	// Relevant environment variables
	envVars := []string{"DOCKER_HOST", "CONTAINER_HOST", "XDG_RUNTIME_DIR", "YOLOAI_SANDBOX", "HOME", "TMUX"}
	for _, key := range envVars {
		if val := os.Getenv(key); val != "" {
			fmt.Fprintf(w, "- **%s:** `%s`\n", key, val) //nolint:errcheck
		}
	}

	// yoloai data directory and disk usage
	dataDir := config.YoloaiDir()
	fmt.Fprintf(w, "- **Data dir:** `%s`\n", dataDir) //nolint:errcheck
	if size, err := sandbox.DirSize(dataDir); err == nil {
		fmt.Fprintf(w, "- **Disk usage:** %s\n", sandbox.FormatSize(size)) //nolint:errcheck
	}

	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportBackends writes section 4: backend availability and versions.
func writeBugReportBackends(ctx context.Context, w io.Writer) {
	fmt.Fprintln(w, "<details>")                   //nolint:errcheck
	fmt.Fprintln(w, "<summary>Backends</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                //nolint:errcheck

	type backendEntry struct {
		name        string
		binary      string
		versionArgs []string
	}

	backends := []backendEntry{
		{"docker", "docker", []string{"version", "--format", "Client: {{.Client.Version}} / Server: {{.Server.Version}}"}},
		{"podman", "podman", []string{"version", "--format", "{{.Client.Version}}"}},
		{"tart", "tart", []string{"--version"}},
		{"seatbelt", "", nil}, // built-in
	}

	for _, b := range backends {
		available, note := checkBackend(ctx, b.name)
		status := "available"
		if !available {
			status = "unavailable"
		}

		var versionStr string
		if b.binary == "" {
			versionStr = "built-in"
		} else if available {
			out, err := exec.CommandContext(ctx, b.binary, b.versionArgs...).Output() //nolint:gosec // args are static constants
			if err == nil {
				versionStr = strings.TrimSpace(string(out))
			} else {
				versionStr = "(version check failed)"
			}
		}

		switch {
		case note != "":
			fmt.Fprintf(w, "- **%s:** %s — %s\n", b.name, status, note) //nolint:errcheck
		case versionStr != "":
			fmt.Fprintf(w, "- **%s:** %s — %s\n", b.name, status, versionStr) //nolint:errcheck
		default:
			fmt.Fprintf(w, "- **%s:** %s\n", b.name, status) //nolint:errcheck
		}
	}

	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportConfig writes section 5: configuration files.
func writeBugReportConfig(w io.Writer, reportType string) {
	fmt.Fprintln(w, "<details>")                        //nolint:errcheck
	fmt.Fprintln(w, "<summary>Configuration</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                     //nolint:errcheck

	writeConfigBlock := func(label, path string, readFn func() ([]byte, error)) {
		fmt.Fprintf(w, "**%s** (`%s`):\n\n", label, path) //nolint:errcheck
		data, err := readFn()
		if err != nil || len(data) == 0 {
			fmt.Fprintln(w, "*(not found)*") //nolint:errcheck
		} else {
			if reportType == "safe" {
				data = sanitizeYAMLConfig(data)
			}
			fmt.Fprintln(w, "```yaml") //nolint:errcheck
			fmt.Fprintf(w, "%s", data) //nolint:errcheck
			fmt.Fprintln(w, "```")     //nolint:errcheck
		}
		fmt.Fprintln(w) //nolint:errcheck
	}

	writeConfigBlock("Global config", config.GlobalConfigPath(), config.ReadGlobalConfigRaw)
	writeConfigBlock("Profile config", config.ConfigPath(), config.ReadConfigRaw)

	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportLiveLog writes section 13: live log captured during the run.
func writeBugReportLiveLog(w io.Writer, logBytes []byte, reportType string) {
	if len(bytes.TrimSpace(logBytes)) == 0 {
		return
	}
	fmt.Fprintln(w, "<details>")                   //nolint:errcheck
	fmt.Fprintln(w, "<summary>Live log</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                //nolint:errcheck
	fmt.Fprintln(w, "```")                         //nolint:errcheck

	if reportType == "safe" {
		sanitized := sanitizeJSONLBytes(logBytes, nil)
		fmt.Fprintf(w, "%s", sanitized) //nolint:errcheck
	} else {
		fmt.Fprintf(w, "%s", logBytes) //nolint:errcheck
	}

	fmt.Fprintln(w, "```")        //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBugReportExit writes section 14: exit code and error.
func writeBugReportExit(w io.Writer, code int, runErr error, panicked bool) {
	switch {
	case panicked:
		fmt.Fprintf(w, "**Exit code:** (panic)\n") //nolint:errcheck
	case runErr != nil:
		fmt.Fprintf(w, "**Exit code:** %d — %s\n", code, runErr) //nolint:errcheck
	default:
		fmt.Fprintf(w, "**Exit code:** %d\n", code) //nolint:errcheck
	}
}

// --- Sanitization ---

// sensitiveYAMLKeywords are key name substrings that trigger YAML value redaction.
var sensitiveYAMLKeywords = []string{
	"key", "token", "secret", "password", "credential", "passwd", "pwd",
	"auth", "jwt", "bearer", "cert", "private", "access", "encryption",
	"saml", "oauth", "sso", "connection",
}

// sanitizeYAMLConfig redacts values for YAML keys whose names contain sensitive keywords.
// Operates line-by-line on raw YAML text (no parser dependency).
func sanitizeYAMLConfig(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		result = append(result, redactYAMLLine(line))
	}
	return []byte(strings.Join(result, "\n"))
}

// redactYAMLLine redacts the value portion of a YAML key: value line if the key is sensitive.
func redactYAMLLine(line string) string {
	// Find key: value pattern (with optional leading whitespace)
	colonIdx := strings.Index(line, ":")
	if colonIdx < 0 {
		return line
	}
	key := strings.TrimSpace(line[:colonIdx])
	keyLower := strings.ToLower(key)
	for _, kw := range sensitiveYAMLKeywords {
		if strings.Contains(keyLower, kw) {
			// Redact everything after the colon
			prefix := line[:colonIdx+1]
			return prefix + " [REDACTED]"
		}
	}
	return line
}

// sanitizeTextPatterns are ordered regex patterns for text sanitization.
var sanitizeTextPatterns = []*regexp.Regexp{
	// PEM blocks
	regexp.MustCompile(`-----BEGIN [A-Z ]+-----[\s\S]*?-----END [A-Z ]+-----`),
	// Known API key prefixes
	regexp.MustCompile(`(?:sk-ant-|sk-proj-|ghp_|ghu_|gha_|sk_live_|sk_test_|AIzaSy|pplx-|gsk_)\S+`),
	// AWS access keys
	regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
	// Connection strings with credentials
	regexp.MustCompile(`\w+://[^:@\s]+:[^@\s]+@\S+`),
	// JWT tokens
	regexp.MustCompile(`eyJ[A-Za-z0-9\-_]{10,}\.[A-Za-z0-9\-_]{10,}\.[A-Za-z0-9\-_]{10,}`),
	// Long hex strings (32+ chars)
	regexp.MustCompile(`[a-fA-F0-9]{32,}`),
	// Long base64 strings (40+ chars)
	regexp.MustCompile(`[A-Za-z0-9+/\-_]{40,}={0,2}`),
}

// sanitizeText applies pattern-based redaction to a text string.
// First match wins (patterns are applied in order).
func sanitizeText(text string) string {
	for _, re := range sanitizeTextPatterns {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// sanitizeJSONLBytes sanitizes JSONL content. If omitEvents is non-nil, lines matching
// those event patterns are removed. String values are sanitized in all modes.
func sanitizeJSONLBytes(data []byte, omitEvents []string) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		// Check if this event should be omitted
		if len(omitEvents) > 0 {
			event := extractJSONLEvent(trimmed)
			if shouldOmitEvent(event, omitEvents) {
				continue
			}
		}
		// Sanitize string values
		sanitized := sanitizeJSONLLine(trimmed)
		out.Write(sanitized)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// extractJSONLEvent extracts the "event" field value from a JSONL line.
func extractJSONLEvent(line []byte) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return ""
	}
	if v, ok := obj["event"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	return ""
}

// shouldOmitEvent returns true if the event matches any omitEvents pattern.
// Pattern ending in ".*" does prefix match; otherwise exact match.
func shouldOmitEvent(event string, omitEvents []string) bool {
	for _, pattern := range omitEvents {
		if strings.HasSuffix(pattern, ".*") {
			prefix := strings.TrimSuffix(pattern, ".*")
			if strings.HasPrefix(event, prefix) {
				return true
			}
		} else if event == pattern {
			return true
		}
	}
	return false
}

// sanitizeJSONLLine parses a JSONL line, sanitizes all string values, and re-serializes.
// Malformed lines are returned unmodified.
func sanitizeJSONLLine(line []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(line, &obj); err != nil {
		return line // pass through malformed lines unchanged
	}
	for k, v := range obj {
		var s string
		if json.Unmarshal(v, &s) == nil {
			sanitized := sanitizeText(s)
			if sanitized != s {
				newVal, err := json.Marshal(sanitized)
				if err == nil {
					obj[k] = newVal
				}
			}
		}
	}
	result, err := json.Marshal(obj)
	if err != nil {
		return line
	}
	return result
}
