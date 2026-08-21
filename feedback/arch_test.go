// ABOUTME: The claim that makes this package usable from the bottom layers —
// ABOUTME: feedback imports nothing else in yoloAI, so runtime/, store/ and
// ABOUTME: copyflow/ can emit without importing anything above them and without
// ABOUTME: an import cycle. Cited from architecture/code-map.md (rule 13).

package feedback_test

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestArch_FeedbackHasNoYoloaiDependencies fails if this package acquires a
// transitive dependency on anything else in the module.
//
// The claim is load-bearing rather than tidy: every layer emits feedback, so
// the emission API must sit below all of them. The first import of, say,
// internal/config would be legal, would compile, and would make the package
// unusable from runtime/ the moment runtime/ was converted — surfacing as an
// import cycle in an unrelated change, with the fix nowhere near the error.
func TestArch_FeedbackHasNoYoloaiDependencies(t *testing.T) {
	const modulePath = "github.com/kstenerud/yoloai"
	const self = modulePath + "/feedback"

	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps}
	pkgs, err := packages.Load(cfg, self)
	if err != nil {
		t.Fatalf("packages.Load(%q): %v", self, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("loaded %d packages, want 1", len(pkgs))
	}

	var offenders []string
	seen := map[string]bool{}
	var walk func(p *packages.Package)
	walk = func(p *packages.Package) {
		for path, imported := range p.Imports {
			if seen[path] {
				continue
			}
			seen[path] = true
			if path == modulePath || strings.HasPrefix(path, modulePath+"/") {
				offenders = append(offenders, path)
			}
			walk(imported)
		}
	}
	walk(pkgs[0])

	if len(offenders) > 0 {
		t.Errorf("feedback depends on %v; it must stay stdlib-only so the bottom "+
			"layers (runtime/, store/, copyflow/) can emit without an import cycle",
			offenders)
	}
}
