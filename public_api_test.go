// ABOUTME: Compile-time fence: every exported declaration in the yoloai
// ABOUTME: package must reference only public types (or internal types
// ABOUTME: re-exported here via alias). Catches the F1-class leak.

package yoloai_test

import (
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestPublicAPI_NoInternalLeaks verifies that no exported declaration in
// the yoloai package references a type whose definition lives under
// internal/, unless that type is also re-exported as an alias at the
// yoloai root.
//
// Why this matters: external embedders cannot import internal packages.
// A function with signature `func (c *Client) Create(opts *sandbox.CreateOptions)`
// compiles in internal callers (yoloai's own internal/cli) but NOT in
// external embedders — `sandbox` is `internal/sandbox`, which Go's
// internal-visibility rules forbid. The Client's package doc claims
// external embedders are a supported audience; this test enforces
// that claim. CRITIQUE.md §F1 is the motivating finding.
//
// Type aliases (`type BackendName = runtime.BackendName`) ARE allowed
// here — they re-publish the internal type under a yoloai-root name,
// so an embedder writing `var b yoloai.BackendName` compiles even
// though the type's home is `internal/runtime`. Direct references to
// an internal type that has no public alias are not allowed.
func TestPublicAPI_NoInternalLeaks(t *testing.T) {
	const modulePath = "github.com/kstenerud/yoloai"
	const internalMarker = "/internal/"

	pkg := loadYoloaiPackage(t, modulePath)
	aliased := collectAliasedInternalTypes(pkg, internalMarker)
	leaks := findInternalLeaks(pkg, internalMarker, aliased)
	unexpected, stale := classifyAgainstBaseline(leaks, f1KnownLeaks)

	if len(unexpected) == 0 && len(stale) == 0 {
		return
	}
	t.Error(formatLeakReport(unexpected, stale))
}

// loadYoloaiPackage loads the yoloai root package via go/packages.
// Test fails fast on any load error.
func loadYoloaiPackage(t *testing.T, modulePath string) *packages.Package {
	t.Helper()
	cfg := &packages.Config{Mode: packages.NeedTypes | packages.NeedName}
	pkgs, err := packages.Load(cfg, modulePath)
	if err != nil {
		t.Fatalf("packages.Load(%q): %v", modulePath, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	pkg := pkgs[0]
	if pkg.PkgPath != modulePath {
		t.Fatalf("expected %q, got %q", modulePath, pkg.PkgPath)
	}
	if len(pkg.Errors) > 0 {
		for _, e := range pkg.Errors {
			t.Errorf("package load error: %v", e)
		}
		t.FailNow()
	}
	return pkg
}

// collectAliasedInternalTypes walks exported TypeNames in the package
// and records any whose underlying type is internal — embedders can
// reach those through the yoloai-root alias, so they don't count as
// leaks at usage sites.
func collectAliasedInternalTypes(pkg *packages.Package, marker string) map[string]bool {
	aliased := make(map[string]bool)
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		tn, ok := obj.(*types.TypeName)
		if !ok || !tn.IsAlias() {
			continue
		}
		if key := internalTypeKey(tn.Type(), marker); key != "" {
			aliased[key] = true
		}
	}
	return aliased
}

// findInternalLeaks walks every exported declaration in pkg's scope and
// collects internal-package type references that aren't covered by
// aliased.
func findInternalLeaks(pkg *packages.Package, marker string, aliased map[string]bool) map[string][]string {
	leaks := make(map[string][]string)
	report := func(typ types.Type, site string) {
		key := internalTypeKey(typ, marker)
		if key == "" || aliased[key] {
			return
		}
		leaks[key] = append(leaks[key], site)
	}
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		walkObj(obj, marker, report)
	}
	return leaks
}

// walkObj dispatches an exported scope object to the right walker.
func walkObj(obj types.Object, marker string, report func(types.Type, string)) {
	switch o := obj.(type) {
	case *types.Func:
		walkSignature(o.Type().(*types.Signature), "func "+o.Name(), marker, report)
	case *types.TypeName:
		if o.IsAlias() {
			return
		}
		walkNamed(o.Type(), "type "+o.Name(), marker, report, make(map[string]bool))
	case *types.Var:
		report(o.Type(), "var "+o.Name())
	case *types.Const:
		report(o.Type(), "const "+o.Name())
	}
}

// classifyAgainstBaseline splits a leak map into (unexpected, stale).
// Unexpected: leaks not on the baseline → regression, fail loudly.
// Stale: baseline entries that no longer leak → fix the baseline so the
// list stays honest as types are migrated.
func classifyAgainstBaseline(leaks map[string][]string, baseline map[string]struct{}) (unexpected map[string][]string, stale map[string]bool) {
	unexpected = make(map[string][]string)
	stale = make(map[string]bool)
	for k := range baseline {
		stale[k] = true
	}
	for k, sites := range leaks {
		if _, accepted := baseline[k]; accepted {
			delete(stale, k)
			continue
		}
		unexpected[k] = sites
	}
	return unexpected, stale
}

// formatLeakReport builds the test-failure message from unexpected
// leaks + stale baseline entries.
func formatLeakReport(unexpected map[string][]string, stale map[string]bool) string {
	var msg strings.Builder
	if len(unexpected) > 0 {
		msg.WriteString("NEW public-API leak (not on the CRITIQUE.md F1 baseline):\n")
		msg.WriteString("external embedders cannot import internal/ packages. Either re-export\n")
		msg.WriteString("each type as a yoloai-root alias (the BackendName pattern in names.go)\n")
		msg.WriteString("or promote it to a new public yoloai-root type. If this is intentional\n")
		msg.WriteString("for now, add the type's key to f1KnownLeaks at the bottom of this file.\n\n")
		writeLeakLines(&msg, unexpected)
	}
	if len(stale) > 0 {
		msg.WriteString("Stale f1KnownLeaks entries (type no longer leaking; remove from baseline):\n")
		for k := range stale {
			msg.WriteString("  ")
			msg.WriteString(k)
			msg.WriteString("\n")
		}
	}
	return msg.String()
}

// writeLeakLines formats a leak map deterministically into msg.
func writeLeakLines(msg *strings.Builder, leaks map[string][]string) {
	keys := make([]string, 0, len(leaks))
	for k := range leaks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sites := leaks[k]
		sort.Strings(sites)
		// De-dupe sites; a type can be referenced in several places.
		seen := make(map[string]bool)
		uniq := sites[:0]
		for _, s := range sites {
			if seen[s] {
				continue
			}
			seen[s] = true
			uniq = append(uniq, s)
		}
		msg.WriteString("  ")
		msg.WriteString(k)
		msg.WriteString("\n")
		for _, s := range uniq {
			msg.WriteString("    referenced from ")
			msg.WriteString(s)
			msg.WriteString("\n")
		}
	}
}

// f1KnownLeaks is the F1 baseline: internal types the public yoloai
// surface is allowed to expose. It is intentionally EMPTY — F1 is closed.
// The public surface now mirrors every previously-leaked internal type
// with a hand-written public type (e.g. config.MergedConfig and its nested
// tree are mirrored by ResolvedProfileConfig and friends in profile_config.go).
//
// Keep this empty. Any new entry re-opens F1 and requires a CRITIQUE.md
// update justifying the regression; the right fix is almost always a
// public mirror type, not a grandfathered leak.
//
// Format: package-path "." TypeName. Match must be exact (the test
// computes the same key from the type's TypeName).
var f1KnownLeaks = map[string]struct{}{}

// internalTypeKey returns a stable identifier for a type IF it's a Named
// type whose package path contains `marker` ("/internal/"). Returns the
// empty string for any other type — the caller treats that as "not a
// leak candidate." Pointers, slices, maps, etc. are unwrapped first.
func internalTypeKey(t types.Type, marker string) string {
	switch tt := t.(type) {
	case *types.Alias:
		// Go 1.22+ (default since 1.24) materializes `type X = pkg.Y` as a
		// *types.Alias rather than collapsing it to the aliased *types.Named.
		// Unwrap so an alias to an internal type keys identically to a direct
		// reference. Without this, collectAliasedInternalTypes records nothing
		// (every alias TypeName's Type() is *types.Alias), and any site that
		// surfaces the underlying Named — e.g. a const whose value type is the
		// aliased type — is falsely flagged as a leak the alias already covers.
		return internalTypeKey(types.Unalias(tt), marker)
	case *types.Named:
		obj := tt.Obj()
		if obj.Pkg() == nil { // builtin (error, etc.)
			return ""
		}
		path := obj.Pkg().Path()
		if !strings.Contains(path, marker) {
			return ""
		}
		return path + "." + obj.Name()
	case *types.Pointer:
		return internalTypeKey(tt.Elem(), marker)
	case *types.Slice:
		return internalTypeKey(tt.Elem(), marker)
	case *types.Array:
		return internalTypeKey(tt.Elem(), marker)
	case *types.Chan:
		return internalTypeKey(tt.Elem(), marker)
	}
	return ""
}

// walkSignature checks each parameter and result type for internal leaks.
func walkSignature(sig *types.Signature, site, marker string, report func(types.Type, string)) {
	walkTuple(sig.Params(), site+" param", marker, report)
	walkTuple(sig.Results(), site+" result", marker, report)
}

// walkTuple iterates a parameter/result tuple, descending into the
// type structure for each element.
func walkTuple(tup *types.Tuple, site, marker string, report func(types.Type, string)) {
	for i := 0; i < tup.Len(); i++ {
		walkType(tup.At(i).Type(), site, marker, report, make(map[string]bool))
	}
}

// walkType recursively descends into a type, calling report() at every
// Named-type leaf so the caller can decide whether it's a leak. visited
// guards against infinite recursion on self-referential types.
func walkType(t types.Type, site, marker string, report func(types.Type, string), visited map[string]bool) {
	switch tt := t.(type) {
	case *types.Named:
		// Report this named type itself; the caller decides if it's a leak.
		report(tt, site)
		// Don't descend into a named type's underlying struct here — the
		// embedder uses the type as a unit; the test should flag the type
		// itself, not its private fields. (We DO descend for struct types
		// declared inline as a function parameter, but those are rare.)
		// However, we do descend into the type's methods if they're
		// exported and the receiver is exported. Method walking happens
		// from the top-level *Func case, so we don't recurse here.
		return
	case *types.Pointer:
		walkType(tt.Elem(), site, marker, report, visited)
	case *types.Slice:
		walkType(tt.Elem(), site, marker, report, visited)
	case *types.Array:
		walkType(tt.Elem(), site, marker, report, visited)
	case *types.Chan:
		walkType(tt.Elem(), site, marker, report, visited)
	case *types.Map:
		walkType(tt.Key(), site+" mapkey", marker, report, visited)
		walkType(tt.Elem(), site+" mapelem", marker, report, visited)
	case *types.Struct:
		// Inline struct — descend into its exported fields.
		for i := 0; i < tt.NumFields(); i++ {
			f := tt.Field(i)
			if !f.Exported() {
				continue
			}
			walkType(f.Type(), site+" field "+f.Name(), marker, report, visited)
		}
	case *types.Signature:
		walkSignature(tt, site, marker, report)
	case *types.Interface:
		for i := 0; i < tt.NumExplicitMethods(); i++ {
			m := tt.ExplicitMethod(i)
			if !m.Exported() {
				continue
			}
			walkSignature(m.Type().(*types.Signature), site+" method "+m.Name(), marker, report)
		}
	}
}

// walkNamed handles a named type declared at package scope: descend into
// its fields/methods to check whether they reference internal types in
// ways that would leak to an external embedder.
func walkNamed(t types.Type, site, marker string, report func(types.Type, string), visited map[string]bool) {
	named, ok := t.(*types.Named)
	if !ok {
		walkType(t, site, marker, report, visited)
		return
	}
	if visited[site] {
		return
	}
	visited[site] = true

	// Walk methods on the named type (pointer and value receiver sets).
	for i := 0; i < named.NumMethods(); i++ {
		m := named.Method(i)
		if !m.Exported() {
			continue
		}
		walkSignature(m.Type().(*types.Signature), site+"."+m.Name(), marker, report)
	}

	// Descend into the type's underlying struct (if any) to check
	// exported field types.
	if st, ok := named.Underlying().(*types.Struct); ok {
		for i := 0; i < st.NumFields(); i++ {
			f := st.Field(i)
			if !f.Exported() {
				continue
			}
			walkType(f.Type(), site+"."+f.Name(), marker, report, visited)
		}
	}
}
