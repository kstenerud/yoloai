// ABOUTME: Typed enum for the per-sandbox JSONL log streams (cli, sandbox,
// ABOUTME: monitor, hooks). Each constant pairs with a *JSONLPath helper.

package store

// LogSource names a structured-log stream emitted by one of yoloai's
// components into the per-sandbox logs directory. Closed set — adding
// a source requires both a constant here and a producer in the
// implementation, by design.
//
// The constants pair with the *JSONLPath helpers (CLIJSONLPath,
// SandboxJSONLPath, MonitorJSONLPath, HooksJSONLPath) defined in this
// package; see those for the on-disk paths.
//
// Established by W-L8a Q-Y. The public Client API surface (added in
// W-L8b/c/d) uses []LogSource for LogOptions.Sources rather than
// []string.
type LogSource string

const (
	LogSourceCLI     LogSource = "cli"     // CLI emits its own structured log; useful for "what did the user run"
	LogSourceSandbox LogSource = "sandbox" // sandbox lifecycle events from the in-container entrypoint
	LogSourceMonitor LogSource = "monitor" // agent idle/active detector emissions from the Python monitor
	LogSourceHooks   LogSource = "hooks"   // hook-emitted events (Claude Code hooks; future agents may emit here too)
)
