package bugreport

// ABOUTME: Bug report section writers and sanitization helpers.
// ABOUTME: Shared by --bugreport flag (flight recorder) and sandbox bugreport command.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
)

// Filename generates the output filename for a bug report.
// Returns an error if a file with the same name already exists.
//
// The PID disambiguates concurrent or rapid-sequential invocations: the
// millisecond timestamp alone collides when two yoloai processes start in the
// same dir within the same millisecond (e.g. a parallel test matrix, or a user
// scripting concurrent --bugreport runs). Each invocation is its own process,
// so its PID is a stable per-invocation discriminator.
func Filename(t time.Time) (string, error) {
	name := fmt.Sprintf("yoloai-bugreport-%s-%d.md",
		t.UTC().Format("20060102-150405.000"), os.Getpid())
	if _, err := os.Stat(name); err == nil {
		return "", fmt.Errorf("file already exists: %s", name)
	}
	return name, nil
}

// WriteHeader writes section 1: report header.
func WriteHeader(w io.Writer, version, commit, date, reportType string) {
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

// WriteCommandInvocation writes section 2: the full command invocation.
// In safe mode, --prompt / -p values are redacted.
func WriteCommandInvocation(w io.Writer, reportType string) {
	args := os.Args
	if reportType == "safe" {
		args = redactPromptArgs(args)
	}
	cmd := strings.Join(args, " ")
	fmt.Fprintf(w, "**Command:** `%s`\n\n", cmd) //nolint:errcheck,gosec // G705: cmd is constructed from os.Args which are caller-controlled, not attacker-controlled
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

// WriteDiagnostics renders sections 3–5 (System, Backends, VM slots, Config)
// from a structured snapshot gathered by the library. The library is the
// mechanism (it gathers the facts); this renderer is the policy (it shapes the
// markdown and, in "safe" reports, redacts the raw config) — see
// development-principles.md §2.
func WriteDiagnostics(w io.Writer, d yoloai.Diagnostics, reportType string) {
	writeSystemSection(w, d.System)
	writeBackendsSection(w, d.Backends)
	writeVMCensusSection(w, d.VMSlots)
	writeConfigSection(w, d.Config, reportType)
}

// writeSystemSection writes section 3: system information.
func writeSystemSection(w io.Writer, sys yoloai.SystemDiagnostics) {
	fmt.Fprintln(w, "<details>")                 //nolint:errcheck
	fmt.Fprintln(w, "<summary>System</summary>") //nolint:errcheck
	fmt.Fprintln(w)                              //nolint:errcheck

	fmt.Fprintf(w, "- **OS/Arch:** %s/%s\n", sys.OS, sys.Arch) //nolint:errcheck

	if sys.Kernel != "" {
		fmt.Fprintf(w, "- **Kernel:** %s\n", sys.Kernel) //nolint:errcheck
	}

	for _, ev := range sys.Env {
		fmt.Fprintf(w, "- **%s:** `%s`\n", ev.Key, ev.Value) //nolint:errcheck
	}

	fmt.Fprintf(w, "- **Data dir:** `%s`\n", sys.DataDir) //nolint:errcheck
	if sys.DiskUsageBytes >= 0 {
		fmt.Fprintf(w, "- **Disk usage:** %s\n", cliutil.FormatSize(sys.DiskUsageBytes)) //nolint:errcheck
	}

	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeBackendsSection writes section 4: backend availability and versions.
func writeBackendsSection(w io.Writer, backends []yoloai.BackendDiagnostic) {
	fmt.Fprintln(w, "<details>")                   //nolint:errcheck
	fmt.Fprintln(w, "<summary>Backends</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                //nolint:errcheck

	for _, b := range backends {
		status := "available"
		if !b.Available {
			status = "unavailable"
		}

		var versionStr string
		if b.Available {
			versionStr = b.Version
			if versionStr == "" {
				versionStr = "(version check failed)"
			}
		}

		switch {
		case b.Note != "":
			fmt.Fprintf(w, "- **%s:** %s — %s\n", b.Type, status, b.Note) //nolint:errcheck
		case versionStr != "":
			fmt.Fprintf(w, "- **%s:** %s — %s\n", b.Type, status, versionStr) //nolint:errcheck
		default:
			fmt.Fprintf(w, "- **%s:** %s\n", b.Type, status) //nolint:errcheck
		}
	}

	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeVMCensusSection writes the host VM-slot census (macOS
// Virtualization.framework concurrency). It is omitted entirely on
// platforms/backends without a census (census == nil) so the report stays
// brief. When present, the slot occupancy is high-value: a reached limit is a
// common, hard-to-diagnose cause of "VM won't start" failures in the wild — see
// the doctor "VM slots" section and the orphaned-VM idiosyncrasy.
func writeVMCensusSection(w io.Writer, census *yoloai.VMCensus) {
	if census == nil {
		return
	}

	fmt.Fprintln(w, "<details>")                   //nolint:errcheck
	fmt.Fprintln(w, "<summary>VM slots</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                //nolint:errcheck

	status := "ok"
	if census.Blocked() {
		status = "LIMIT REACHED — blocks new VMs"
	}
	fmt.Fprintf(w, "- **In use:** %d of %d (%s)\n", census.InUse(), census.Limit, status) //nolint:errcheck
	for _, s := range census.Slots {
		fmt.Fprintf(w, "  - %s\n", vmSlotLine(s)) //nolint:errcheck
	}

	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// writeConfigSection writes section 5: configuration files. In "safe" reports,
// the raw config bytes are redacted before rendering.
func writeConfigSection(w io.Writer, cfg yoloai.ConfigDiagnostics, reportType string) {
	fmt.Fprintln(w, "<details>")                        //nolint:errcheck
	fmt.Fprintln(w, "<summary>Configuration</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                     //nolint:errcheck

	writeConfigBlock := func(label, path string, data []byte) {
		fmt.Fprintf(w, "**%s** (`%s`):\n\n", label, path) //nolint:errcheck
		if len(data) == 0 {
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

	writeConfigBlock("Global config", cfg.GlobalPath, cfg.GlobalRaw)
	writeConfigBlock("Profile config", cfg.ProfilePath, cfg.ProfileRaw)

	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// vmSlotLine renders one VM slot as a compact bug-report line.
func vmSlotLine(s yoloai.VMSlot) string {
	name := s.VMName
	if name == "" {
		name = "(unknown)"
	}
	switch {
	case s.Owned:
		return fmt.Sprintf("pid %d  %s — owned sandbox", s.PID, name)
	case s.Deleted:
		return fmt.Sprintf("pid %d  %s — orphan (image deleted), holding a slot", s.PID, name)
	default:
		return fmt.Sprintf("pid %d  %s — orphan (launcher gone), holding a slot", s.PID, name)
	}
}

// WriteLiveLog writes section 13: live log captured during the run.
func WriteLiveLog(w io.Writer, logBytes []byte, reportType string) {
	if len(bytes.TrimSpace(logBytes)) == 0 {
		return
	}
	fmt.Fprintln(w, "<details>")                   //nolint:errcheck
	fmt.Fprintln(w, "<summary>Live log</summary>") //nolint:errcheck
	fmt.Fprintln(w)                                //nolint:errcheck
	fmt.Fprintln(w, "```")                         //nolint:errcheck

	if reportType == "safe" {
		sanitized := SanitizeJSONLBytes(logBytes, nil, true)
		fmt.Fprintf(w, "%s", sanitized) //nolint:errcheck
	} else {
		fmt.Fprintf(w, "%s", logBytes) //nolint:errcheck
	}

	fmt.Fprintln(w, "```")        //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
	fmt.Fprintln(w, "</details>") //nolint:errcheck
	fmt.Fprintln(w)               //nolint:errcheck
}

// WriteExit writes section 14: exit code and error.
func WriteExit(w io.Writer, code int, runErr error, panicked bool) {
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
	// Long base64 strings (40+ chars). The charset deliberately excludes '/'
	// so this does not collapse ordinary filesystem paths (e.g. a long
	// /Users/.../project/... path) to [REDACTED] — paths are prime
	// diagnostic data. Standard-base64 secrets that contain '/' but no
	// recognizable prefix are the only gap, and those are rare.
	regexp.MustCompile(`[A-Za-z0-9+\-_]{40,}={0,2}`),
}

// sanitizeText applies pattern-based redaction to a text string. Every pattern
// runs in turn, each over the result of the previous replacement.
func sanitizeText(text string) string {
	for _, re := range sanitizeTextPatterns {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// SanitizeJSONLBytes filters JSONL content. If omitEvents is non-nil, lines
// matching those event patterns are removed. When redactText is true, string
// values are run through the secret-redaction patterns; unsafe reports pass
// false so the report stays a faithful, unredacted record.
func SanitizeJSONLBytes(data []byte, omitEvents []string, redactText bool) []byte {
	var out bytes.Buffer
	for line := range bytes.SplitSeq(data, []byte("\n")) {
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
		if redactText {
			trimmed = sanitizeJSONLLine(trimmed)
		}
		out.Write(trimmed)
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
		if before, ok := strings.CutSuffix(pattern, ".*"); ok {
			prefix := before
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
