// ABOUTME: Pins the frozen pre-tier paths to literal strings, so that expressing
// ABOUTME: them in terms of the live layout builders fails here instead of silently.
package pretier

import (
	"path/filepath"
	"testing"
)

// The assertions below are spelled as whole literal paths on purpose. This
// package's contract is that it does NOT track the current layout: every helper
// must keep returning the flat, pre-v6 location however far the live builders
// move. Deriving the expectations here from internal/config — or from the
// helpers themselves — would make this test agree with any future move, which is
// precisely the failure it exists to catch (DF164, standards/go.md "A migrator
// addresses the layout of its own era").
func TestPathsAreFlatLiterals(t *testing.T) {
	const sandboxDir = "/data/sandboxes/box"
	const enc = "^home^user^proj"

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"environment", EnvironmentPath(sandboxDir), "/data/sandboxes/box/environment.json"},
		{"sandbox state", SandboxStatePath(sandboxDir), "/data/sandboxes/box/sandbox-state.json"},
		{"agent config", AgentConfigPath(sandboxDir), "/data/sandboxes/box/agent.json"},
		{"netpolicy", NetpolicyPath(sandboxDir), "/data/sandboxes/box/netpolicy.json"},
		{"runtime config", RuntimeConfigPath(sandboxDir), "/data/sandboxes/box/runtime-config.json"},
		{"work base", WorkDir(sandboxDir), "/data/sandboxes/box/work"},
		{"work dir for a mount", WorkDirFor(sandboxDir, enc), "/data/sandboxes/box/work/" + enc},
		{"overlay lower", OverlayLowerFor(sandboxDir, enc), "/data/sandboxes/box/work/" + enc + "/lower"},
	}
	for _, tc := range cases {
		if tc.got != filepath.FromSlash(tc.want) {
			t.Errorf("%s = %q, want %q — a pre-tier path must not follow the live layout", tc.name, tc.got, tc.want)
		}
	}
}

// Every record a pre-v6 migrator touches sits directly in the sandbox dir. A
// helper that started nesting one (under host/, say) would break the migrators
// silently, since they report a missing record as "nothing to migrate".
func TestRecordPathsAreDirectChildrenOfTheSandboxDir(t *testing.T) {
	const sandboxDir = "/data/sandboxes/box"
	records := map[string]string{
		"environment":    EnvironmentPath(sandboxDir),
		"sandbox state":  SandboxStatePath(sandboxDir),
		"agent config":   AgentConfigPath(sandboxDir),
		"netpolicy":      NetpolicyPath(sandboxDir),
		"runtime config": RuntimeConfigPath(sandboxDir),
	}
	for name, path := range records {
		if dir := filepath.Dir(path); dir != filepath.FromSlash(sandboxDir) {
			t.Errorf("%s lives in %q, want it directly under %q", name, dir, sandboxDir)
		}
	}
}
