package runtimeconfig

// ABOUTME: Cross-language fence: the Go writer's SchemaVersion must match the
// ABOUTME: Python reader's RUNTIME_CONFIG_SCHEMA_VERSION.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestSchemaVersion_GoPythonAgreement asserts the runtime-config.json schema
// version constant in setup_helpers.py and status-monitor.py matches the
// Go-side SchemaVersion. F25: with three independent definitions (one Go
// const + two Python literals), bumping one and forgetting the others is a
// runtime failure on freshly-created sandboxes. This test fires at every
// `go test ./...` and surfaces the drift before merge.
//
// We could collapse to a single source of truth via `go generate` writing
// a Python file, but the Python files are small and the test is cheaper.
// If the constant moves around more frequently in the future, switch to
// code generation; until then this is the lightweight solution.
func TestSchemaVersion_GoPythonAgreement(t *testing.T) {
	// Locate the runtime/monitor directory relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))) // runtimeconfig -> sandbox -> internal -> repo
	monitorDir := filepath.Join(repoRoot, "runtime", "monitor")

	cases := []struct {
		file    string
		pattern string // regex with one capturing group for the integer
	}{
		{
			file:    "setup_helpers.py",
			pattern: `(?m)^RUNTIME_CONFIG_SCHEMA_VERSION:\s*int\s*=\s*(\d+)`,
		},
		{
			file:    "status-monitor.py",
			pattern: `runtime_config_schema_version\s*=\s*(\d+)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(monitorDir, tc.file)) //nolint:gosec // path constructed from test file location
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			re := regexp.MustCompile(tc.pattern)
			m := re.FindStringSubmatch(string(data))
			if m == nil {
				t.Fatalf("could not find schema-version assignment in %s (pattern %q)", tc.file, tc.pattern)
			}
			pyVal, err := strconv.Atoi(strings.TrimSpace(m[1]))
			if err != nil {
				t.Fatalf("parse schema version from %s (%q): %v", tc.file, m[1], err)
			}
			if pyVal != SchemaVersion {
				t.Errorf("schema version drift: %s reads %d, Go SchemaVersion is %d.\n"+
					"Bump both together; see internal/orchestrator/runtimeconfig/runtimeconfig.go:%d.",
					tc.file, pyVal, SchemaVersion, findConstLine(repoRoot))
			}
		})
	}
}

// findConstLine returns the line number for SchemaVersion's declaration, for
// the failure message. Best-effort — returns 0 if the const can't be located.
func findConstLine(repoRoot string) int {
	data, err := os.ReadFile(filepath.Join(repoRoot, "internal", "sandbox", "runtimeconfig", "runtimeconfig.go")) //nolint:gosec // test asset
	if err != nil {
		return 0
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "SchemaVersion =") {
			return i + 1
		}
	}
	return 0
}
