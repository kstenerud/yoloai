package status

// ABOUTME: Cross-language fence: every agent-status.json schema_version literal
// ABOUTME: (Go hook commands + Python writers) must match agentStatusSchemaVersion.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestAgentStatusSchemaVersion_CrossLanguageAgreement asserts that every
// independent definition of the agent-status.json schema version agrees with
// the Go source-of-truth agentStatusSchemaVersion. Unlike runtime-config.json
// (one Go const + two Python literals), agent-status.json has *four* writers:
// the Go const here, a named Python constant in sandbox-setup.py, a bare
// literal in status-monitor.py's monitor write, and two bare literals embedded
// in agent.go's shell-hook command strings. Bumping one and forgetting the
// others is a silent runtime divergence on freshly-created sandboxes — a reader
// discards a status file whose version it doesn't recognise. F7 of the
// post-F1-close round extends the existing runtime-config fence to this sibling
// constant so the drift surfaces at every `go test ./...` before merge.
//
// (As with the runtime-config fence, we could collapse to a single source of
// truth via `go generate`, but the literals are few and stable; the test is
// the cheaper instrument until that stops being true.)
func TestAgentStatusSchemaVersion_CrossLanguageAgreement(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// status -> sandbox -> internal -> repo
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))

	cases := []struct {
		path            string // relative to repoRoot
		pattern         string // regex with one capturing group for the integer
		expectedMatches int    // how many literals we expect to find (guards a silently-stale regex)
	}{
		{
			path:            filepath.Join("runtime", "monitor", "sandbox-setup.py"),
			pattern:         `(?m)^AGENT_STATUS_SCHEMA_VERSION\s*=\s*(\d+)`,
			expectedMatches: 1,
		},
		{
			path:            filepath.Join("runtime", "monitor", "status-monitor.py"),
			pattern:         `"schema_version":\s*(\d+)`,
			expectedMatches: 1,
		},
		{
			path:            filepath.Join("internal", "agent", "agent.go"),
			pattern:         `"schema_version":(\d+)`,
			expectedMatches: 2, // statusIdleCommand + statusActiveCommand
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(repoRoot, tc.path)) //nolint:gosec // path constructed from test file location
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			re := regexp.MustCompile(tc.pattern)
			matches := re.FindAllStringSubmatch(string(data), -1)
			if len(matches) != tc.expectedMatches {
				t.Fatalf("found %d schema-version literal(s) in %s, expected %d (pattern %q) — "+
					"the regex is stale or a writer was added/removed; update this fence",
					len(matches), tc.path, tc.expectedMatches, tc.pattern)
			}
			for _, m := range matches {
				val, err := strconv.Atoi(strings.TrimSpace(m[1]))
				if err != nil {
					t.Fatalf("parse schema version from %s (%q): %v", tc.path, m[1], err)
				}
				if val != agentStatusSchemaVersion {
					t.Errorf("agent-status schema version drift: %s declares %d, Go agentStatusSchemaVersion is %d.\n"+
						"Bump every writer together; see internal/orchestrator/status/status.go (agentStatusSchemaVersion).",
						tc.path, val, agentStatusSchemaVersion)
				}
			}
		})
	}
}
