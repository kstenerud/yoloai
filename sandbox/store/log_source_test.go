// ABOUTME: Tests for LogSource typed-enum constants and their pairing with
// ABOUTME: the *JSONLPath helpers.

package store

import (
	"strings"
	"testing"
)

func TestLogSourceConstants(t *testing.T) {
	cases := []struct {
		got  LogSource
		want string
	}{
		{LogSourceCLI, "cli"},
		{LogSourceSandbox, "sandbox"},
		{LogSourceMonitor, "monitor"},
		{LogSourceHooks, "hooks"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("LogSource(%v) = %q, want %q", c.got, string(c.got), c.want)
		}
	}
}

// TestLogSourcePairsWithPathHelpers checks each LogSource has a *JSONLPath
// helper that produces a path containing the source name. Catches the
// "added a constant but forgot to wire up the path helper" mistake.
func TestLogSourcePairsWithPathHelpers(t *testing.T) {
	const sandboxName = "test-sandbox"
	cases := []struct {
		source LogSource
		path   string
	}{
		{LogSourceCLI, CLIJSONLPath(sandboxName)},
		{LogSourceSandbox, SandboxJSONLPath(sandboxName)},
		{LogSourceMonitor, MonitorJSONLPath(sandboxName)},
		{LogSourceHooks, HooksJSONLPath(sandboxName)},
	}
	for _, c := range cases {
		if !strings.Contains(c.path, string(c.source)) {
			t.Errorf("LogSource %q: path %q does not contain the source name", c.source, c.path)
		}
	}
}
