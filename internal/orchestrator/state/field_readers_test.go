// ABOUTME: Every field on state.State must have a reader somewhere. A field
// ABOUTME: written on every create and read by nobody is invisible — it has no
// ABOUTME: test that can go red — and seven accumulated here before anyone
// ABOUTME: counted. This is what makes the eighth fail instead.

package state_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// stateFieldsWithNoReader is empty, and that is the point. An entry here would
// be a field declared dead on purpose, which is a contradiction — if nothing
// reads it, deleting it changes nothing. It exists so the failure message can
// say that rather than inviting a suppression.
var stateFieldsWithNoReader = map[string]string{}

// TestArch_EveryStateFieldHasAReader fails when a field on state.State is
// assigned but never read.
//
// Why this specific struct gets a gate (DF221): `State` is wide, assembled at
// one site, and consumed at many. Adding a field is free, nothing forces a
// consumer to appear, and — the part that makes it invisible — **a field with
// no reader has no test that can go red.** Seven accumulated before anyone
// counted: `IsolationExplicit` (DF205) and `DevcontainerMountWarnings` (DF219)
// were each found and deleted as one-offs, and five more turned up when the
// third prompted an actual count.
//
// Deleting them without this would have left the generator running, which is
// the whole reason the finding asked what prevents the next one.
//
// The reader search is deliberately textual — `.FieldName` anywhere outside
// this package, minus the assignment sites. That over-approximates: a field
// named like a common word could be credited to an unrelated match. It errs
// toward false negatives (missing a dead field) rather than false positives
// (blocking a live one), which is the right direction for a gate nobody should
// have to argue with.
func TestArch_EveryStateFieldHasAReader(t *testing.T) {
	root := repoRootFromState(t)
	fields := stateFieldNames(t, filepath.Join(root, "internal", "orchestrator", "state", "state.go"))
	if len(fields) < 10 {
		t.Fatalf("found %d fields on state.State — the parse failed, so this gate is checking nothing", len(fields))
	}

	src := goSourceOutsideState(t, root)
	if len(src) < 100 {
		t.Fatalf("scanned %d Go files — too few for the reader search to mean anything", len(src))
	}

	var dead []string
	for _, f := range fields {
		if !hasReader(src, f) {
			dead = append(dead, f)
		}
	}

	sort.Strings(dead)
	for _, f := range dead {
		if _, ok := stateFieldsWithNoReader[f]; ok {
			continue
		}
		t.Errorf("state.State.%s is assigned but never read. A field nothing consumes has no test "+
			"that can go red, which is how seven of these accumulated (DF221). Delete it, or wire "+
			"the consumer it was added for.", f)
	}
	t.Logf("state-field reader gate scope: %d fields across %d Go files", len(fields), len(src))
}

// stateFieldNames returns the field names declared on state.State.
func stateFieldNames(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "State" {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if name.IsExported() {
					names = append(names, name.Name)
				}
			}
		}
		return false
	})
	return names
}

// goSourceOutsideState returns the contents of every tracked .go file in the
// repo except this package's own, keyed by path.
//
// Read directly rather than shelled out to `git grep`: spawning a subprocess
// would need internal/sysexec and an explicit environment (DEV §12), and a
// fence has no business caring about either.
func goSourceOutsideState(t *testing.T, root string) map[string]string {
	t.Helper()
	const selfPkg = "internal/orchestrator/state"

	src := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip VCS and build detritus; nothing there declares a reader.
			if name := d.Name(); name == ".git" || name == "dist" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || strings.HasPrefix(filepath.ToSlash(rel), selfPkg) {
			return nil //nolint:nilerr // an unrelatable path is not a reader; skipping it is the answer
		}
		b, readErr := os.ReadFile(path) //nolint:gosec // G304: path from a walk rooted at the repo
		if readErr != nil {
			return readErr
		}
		src[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo: %v — a fence that cannot read its corpus is checking nothing", err)
	}
	return src
}

// hasReader reports whether any file references `.Field` other than as the
// field's own assignment in a composite literal.
func hasReader(src map[string]string, field string) bool {
	ref := regexp.MustCompile(`\.` + field + `\b`)
	assign := regexp.MustCompile(`^\s*` + field + `:\s`)
	for _, body := range src {
		for line := range strings.SplitSeq(body, "\n") {
			if assign.MatchString(line) {
				continue // `Field: value` is a write
			}
			if ref.MatchString(line) {
				return true
			}
		}
	}
	return false
}

// repoRootFromState walks up to the module root.
func repoRootFromState(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
