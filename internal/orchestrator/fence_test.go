// ABOUTME: Architecture fence — the orchestration layer must not branch on a
// ABOUTME: backend's identity. Decisions key off declared capabilities (e.g.
// ABOUTME: runtime.FilesystemLocality), never on the backend type. See the
// ABOUTME: module-split design doc's "governing rule".
package orchestrator_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// backendIdentityBranch matches a comparison (== / !=) or switch-case against a
// concrete backend-type constant — the "branch on identity" the layers above the
// runtime must not do. They must read a declared capability instead (the property
// set on BackendDescriptor / BackendCaps).
//
// It deliberately does NOT match `BackendType == ""` (an emptiness / "is a backend
// selected?" check), assignments (`= runtime.BackendDocker`, e.g. the v0->v1
// migration default), or passing a backend type as a function argument
// (`runtime.New(ctx, runtime.BackendTart, ...)` in the `yoloai tart` feature
// command) — none of those are identity branches.
var backendIdentityBranch = regexp.MustCompile(
	`(==|!=|case)\s+runtime\.Backend(Tart|Docker|Seatbelt|Apple|Containerd|Podman)\b`)

// TestNoBackendIdentityBranchingInOrchestration scans internal/orchestrator/** (the
// orchestration layer) for backend-identity branches. A new one is a defect: the
// decision belongs on a declared capability, resolved by injection or a property —
// see the FilesystemLocality work. Extend the scope to the library root if the
// same discipline is wanted there.
func TestNoBackendIdentityBranchingInOrchestration(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // walking our own source tree
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // doc references to backend names are fine
			}
			if backendIdentityBranch.MatchString(line) {
				t.Errorf("%s:%d branches on backend identity; key the decision off a declared "+
					"capability (e.g. runtime.FilesystemLocality), not the backend type:\n\t%s",
					path, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestBackendIdentityBranchRegex proves the fence catches identity branches and
// does not flag the legitimate non-branch uses — so the scan above is meaningful,
// not a no-op.
func TestBackendIdentityBranchRegex(t *testing.T) {
	mustMatch := []string{
		"\tif rt.Descriptor().Type == runtime.BackendTart {",
		"\tif b != runtime.BackendDocker {",
		"\tcase runtime.BackendSeatbelt:",
	}
	for _, s := range mustMatch {
		if !backendIdentityBranch.MatchString(s) {
			t.Errorf("fence should flag identity branch but didn't: %q", s)
		}
	}

	mustNotMatch := []string{
		`	if opts.BackendType == "" {`,                                // emptiness / selection check
		"\tmeta.BackendType = runtime.BackendDocker",                  // assignment (migration default)
		"\tr, err := runtime.New(ctx, runtime.BackendTart, a.layout)", // argument (feature command)
		"\tBackendDocker BackendType = runtime.BackendDocker",         // public re-export alias
	}
	for _, s := range mustNotMatch {
		if backendIdentityBranch.MatchString(s) {
			t.Errorf("fence should NOT flag legitimate use but did: %q", s)
		}
	}
}
