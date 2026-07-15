// ABOUTME: Repo-hygiene gates that run under plain `go test ./...` (no build
// ABOUTME: tags), so `make check` enforces on every PR the standing claims that
// ABOUTME: no linter can express. Each gate below states what it enforces and
// ABOUTME: what went wrong without it; grep `func TestRepoHygiene_` for the set.

package yoloai_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// This file enforces the standing claims that nothing else in the build checks.
// The list is deliberately un-numbered: a count here is a copy of a fact the
// file already states, nothing keeps the two in step, and it read "three" while
// four gates were defined below (the same denormalisation D119 removed from
// general-principles.md's ABOUTME).
//
//  1. Every Go/Python(runtime/monitor)/shell(scripts/) source file carries an
//     ABOUTME header, per docs/contributors/standards/markdown.md. Without
//     this, a new file can silently skip the convention forever — nothing
//     else notices; the doc's "100% compliant" claim would rot the moment
//     someone forgets.
//  2. Every `D<n>`/`DF<n>` rationale ID cited from a Go comment resolves to
//     a real entry in decisions/working-notes*.md or design/findings-*.md,
//     and no ID is defined twice. Without this, a citation can silently
//     point at nothing (typo, renumbering, a decision that got deleted) or
//     — worse — point at two different decisions with no way to tell which
//     one the citing comment meant.
//  3. No `//nolint` directive suppresses cyclop/gocognit/gocyclo, and
//     .golangci.yml still pins their thresholds literally. Without this,
//     "extract a function" can quietly become "silence the linter" one
//     `//nolint:cyclop` at a time, or the threshold itself can be loosened
//     — both defeat development-principles.md §10 without anyone noticing
//     until the complexity has already piled up elsewhere.
//  4. Every `YOLOAI_TEST_*` gate the Go code reads is set by something in the
//     tree. Without this, a gate nothing turns on is a deleted test that
//     reports green forever, and no diff ever says so — see DF94, where a
//     whole tier self-skipped for months behind a near-namesake variable, and
//     DF99, where two C1 security tests had never run.
//
// Deliberately NOT gated here: markdown.md also requires ABOUTME headers on
// docs/contributors/**/*.md. 122 of those files are currently missing one —
// a known, tracked bulk-add task, not a per-PR regression. Gating it here
// would make this test permanently red for a gap a separate task owns; once
// that sweep lands, extend Gate A to cover docs/contributors/**/*.md too.
//
// (ABOUTME line width USED to be listed here as deliberately not gated, on the
// grounds that markdown.md stated no width rule. D117 made it a rule at 100
// columns and this file gained aboutmeMaxCols to enforce it, but the paragraph
// saying the opposite stayed — sitting twenty lines above the check that
// refuted it. Removed under D119. A comment that describes what the code does
// not do is the first thing to rot, because nothing fails when it goes wrong.)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// repoRoot walks up from the test binary's working directory until it finds
// go.mod, so this test works whether `go test` is invoked from the repo
// root, a subpackage, or an IDE's own working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	start := dir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up from %q to filesystem root without finding go.mod", start)
		}
		dir = parent
	}
}

// trackedFiles returns every git-tracked path under root, relative to root,
// forward-slash separated. Scoping to tracked files (not a filesystem walk)
// means build output, .git, and anything gitignored never enter the gates.
//
// Runs git via internal/sysexec (DEV §12: the one licensed subprocess site,
// explicit env, never inherited ambient — that ban binds test code too), with
// testutil.GitEnv() for a hermetic PATH.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := sysexec.Command(testutil.GitEnv(), "git", "-C", root, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files
}

// firstLines reads up to n lines from path.
func firstLines(t *testing.T, path string, n int) []string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: path is built from git ls-files output under repoRoot, not attacker input
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on a read-only fd
	var lines []string
	sc := bufio.NewScanner(f)
	for len(lines) < n && sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

// eachLine calls fn with (1-based line number, line text) for every line of
// path.
func eachLine(t *testing.T, path string, fn func(lineNum int, line string)) {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: path is built from git ls-files output under repoRoot, not attacker input
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on a read-only fd
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		fn(n, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
}

// commentSite is a single "//" line comment extracted from a parsed Go
// source file: its 1-based line number and its full text, "//" prefix
// included.
type commentSite struct {
	line int
	text string
}

// goFileComments parses path as Go source and returns every "//" line
// comment in it — doc comments and inline trailing comments alike — each
// with its line number.
//
// This uses go/parser rather than scanning raw text lines for a reason
// proven the hard way: a naive strings.Index(line, "//") scan cannot tell a
// real comment from a "//nolint:..." - or "D<n>"-shaped string that merely
// APPEARS inside a Go string literal. This file's own table-driven test
// fixtures embed exactly that kind of text as data (see the
// nolintComplexityName and citation matcher test cases below), and a
// line-scanning first draft of the real-tree gates below self-hosted false
// positives against this very file before this function existed. Parsing
// the file and reading only genuine *ast.Comment nodes makes that class of
// false positive structurally impossible: string-literal content is never
// visited. See TestRepoHygiene_GoFileComments_IgnoresStringLiterals.
func goFileComments(t *testing.T, path string) []commentSite {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []commentSite
	for _, group := range f.Comments {
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "//") {
				out = append(out, commentSite{line: fset.Position(c.Pos()).Line, text: c.Text})
			}
		}
	}
	return out
}

// TestRepoHygiene_GoFileComments_IgnoresStringLiterals proves goFileComments
// can fail in the way that matters: fed a file whose only "//nolint:..." and
// "D<n>"-shaped text lives inside a backtick string literal, it must not
// report that text as a comment at all — only the two genuine comments (one
// doc comment, one trailing) should come back.
func TestRepoHygiene_GoFileComments_IgnoresStringLiterals(t *testing.T) {
	dir := t.TempDir()
	src := "package fixture\n\n" +
		"// real comment citing D999, safe to suppress: //nolint:gocognit is fake below.\n" +
		"const lookalike = `//nolint:gocognit fake directive; D888 fake citation`\n" +
		"var x = 1 // real trailing comment citing DF999\n"
	path := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	comments := goFileComments(t, path)
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2 (the doc comment and the trailing comment): %+v", len(comments), comments)
	}
	joined := comments[0].text + "\n" + comments[1].text
	if !strings.Contains(joined, "D999") || !strings.Contains(joined, "DF999") {
		t.Errorf("the two genuine comments were not both captured: %+v", comments)
	}
	if strings.Contains(joined, "D888") || strings.Contains(joined, "fake directive") {
		t.Errorf("string-literal content leaked into extracted comments: %+v", comments)
	}
}

// ---------------------------------------------------------------------------
// Gate A: ABOUTME headers (docs/contributors/standards/markdown.md
// "Required" list) on Go, runtime/monitor Python, and scripts/ shell files.
// ---------------------------------------------------------------------------

// hasABOUTMEHeader reports whether an ABOUTME: line appears anywhere in the
// given lines (callers pass the file's first N lines). It is a plain
// substring test — deliberately not a column/prefix match — because the
// comment marker differs by language ("// ABOUTME:" / "# ABOUTME:") and
// markdown.md imposes no width rule on the header (330+ existing lines
// already exceed 80 columns; enforcing width would be inventing a rule
// markdown.md doesn't state).
func hasABOUTMEHeader(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "ABOUTME:") {
			return true
		}
	}
	return false
}

// aboutmeCategory classifies a tracked path into one of the three gated
// buckets, or "" if the file isn't in scope for Gate A.
func aboutmeCategory(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasPrefix(path, "runtime/monitor/") && strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasPrefix(path, "scripts/") && strings.HasSuffix(path, ".sh"):
		return "shell"
	default:
		return ""
	}
}

// TestRepoHygiene_ABOUTMEHeaders_AllTrackedFilesCompliant is Gate A: every
// tracked *.go, runtime/monitor/*.py, and scripts/*.sh file must carry an
// ABOUTME: line in its first 6 lines. Verified at authoring time: Go
// 580/580, Python runtime/monitor 9/9, scripts/*.sh 4/4 — this should be
// GREEN today. A failure here is a real gap (a new file that skipped the
// convention), not a flaky check; there is no allowlist to add to.
// aboutmeMaxCols is the ABOUTME line-width limit from
// docs/contributors/standards/markdown.md, comment marker included.
//
// 100, not 80 (D117). markdown.md said 80, but only inside an example block
// rather than as a rule, and nothing enforced it: 351 lines across 256 files had
// drifted past. Gating at 80 would have meant reflowing a quarter of the repo to
// satisfy a number nobody had ever applied. 100 is what the code already does —
// exactly four lines exceeded it, and those were reflowed, not grandfathered.
const aboutmeMaxCols = 100

func TestRepoHygiene_ABOUTMEHeaders_AllTrackedFilesCompliant(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)

	counted := map[string]int{"go": 0, "python": 0, "shell": 0}
	var missing []string
	var tooWide []string
	for _, rel := range files {
		cat := aboutmeCategory(rel)
		if cat == "" {
			continue
		}
		counted[cat]++
		abs := filepath.Join(root, rel)
		head := firstLines(t, abs, 8)
		if !hasABOUTMEHeader(head) {
			missing = append(missing, rel)
		}
		for _, line := range head {
			// RuneCountInString, not len(): len() counts BYTES, and these comments
			// use em-dashes freely (3 bytes each). A byte count reports a 99-column
			// line as 101 and rejects correct code — it fired on
			// runtime/containerd/exec.go the first time this ran. The limit is columns.
			cols := utf8.RuneCountInString(line)
			if strings.Contains(line, "ABOUTME:") && cols > aboutmeMaxCols {
				tooWide = append(tooWide, fmt.Sprintf("%s (%d cols)", rel, cols))
			}
		}
	}

	t.Logf("Gate A scope: go=%d python(runtime/monitor)=%d shell(scripts/)=%d",
		counted["go"], counted["python"], counted["shell"])

	if len(missing) > 0 {
		sort.Strings(missing)
		var b strings.Builder
		b.WriteString("files missing an ABOUTME: line in their first 6 lines ")
		b.WriteString("(docs/contributors/standards/markdown.md requires one on every ")
		b.WriteString("*.go, runtime/monitor/*.py, and scripts/*.sh file):\n")
		for _, m := range missing {
			b.WriteString("  " + m + "\n")
		}
		b.WriteString("Fix: add an ABOUTME header block at the top of the file.\n")
		t.Error(b.String())
	}

	if len(tooWide) > 0 {
		sort.Strings(tooWide)
		var b strings.Builder
		fmt.Fprintf(&b, "ABOUTME lines wider than %d columns "+
			"(docs/contributors/standards/markdown.md):\n", aboutmeMaxCols)
		for _, w := range tooWide {
			b.WriteString("  " + w + "\n")
		}
		b.WriteString("Fix: wrap onto another ABOUTME line.\n")
		t.Error(b.String())
	}
}

// TestRepoHygiene_ABOUTMEWidth_RejectsOverlongAndCountsRunes proves the width
// half of Gate A can fail, and pins the byte-vs-rune trap it was born from:
// len() counts bytes, these comments are full of em-dashes at 3 bytes each, and
// the first cut rejected a correct 99-column line as 101.
func TestRepoHygiene_ABOUTMEWidth_RejectsOverlongAndCountsRunes(t *testing.T) {
	const marker = "// ABOUTME: "
	cases := []struct {
		name    string
		line    string
		tooWide bool
	}{
		{"comfortably short", marker + "short.", false},
		{"exactly at the limit", marker + strings.Repeat("x", aboutmeMaxCols-len(marker)), false},
		{"one past the limit", marker + strings.Repeat("x", aboutmeMaxCols-len(marker)+1), true},
		{
			// At the limit in columns, over it in bytes: a byte count rejects this.
			"em-dashes are one column each, not three",
			marker + strings.Repeat("\u2014", 2) + strings.Repeat("x", aboutmeMaxCols-len(marker)-2),
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := utf8.RuneCountInString(c.line) > aboutmeMaxCols; got != c.tooWide {
				t.Errorf("width check on a %d-rune/%d-byte line = %v, want %v",
					utf8.RuneCountInString(c.line), len(c.line), got, c.tooWide)
			}
		})
	}
}

// TestRepoHygiene_ABOUTMEMatcher_RejectsBadHeaders proves Gate A's matcher
// can fail: it is a pure table test against literal strings, independent of
// repo content, so a gate that has silently stopped checking anything would
// still be caught here.
func TestRepoHygiene_ABOUTMEMatcher_RejectsBadHeaders(t *testing.T) {
	cases := []struct {
		name string
		want bool
		body []string
	}{
		{"go header present", true, []string{
			"// ABOUTME: does the thing.", "", "package foo",
		}},
		{"python header present", true, []string{
			"# ABOUTME: does the other thing.", "", "import os",
		}},
		{"missing entirely", false, []string{
			"package foo", "", "func main() {}",
		}},
		{"header beyond the 6-line window is not seen by caller", false, []string{
			// firstLines(path, 6) would never hand this slice to the matcher
			// in the real gate; included here to document that the matcher
			// itself only looks at what it's given, the window is the
			// caller's job.
			"package foo", "", "", "", "", "",
		}},
		{"empty file", false, nil},
		{"similar but wrong marker (typo) is rejected", false, []string{
			"// ABOUT: does the thing.",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hasABOUTMEHeader(c.body)
			if got != c.want {
				t.Errorf("hasABOUTMEHeader(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Gate B: D<n>/DF<n> rationale-ID citations resolve, and no ID is defined
// twice.
// ---------------------------------------------------------------------------

// canonicalDHeadingRe matches a decision heading in
// decisions/working-notes.md or working-notes-archive.md, e.g.
// "## D74 — Lazy backend connection". Decisions don't use the DF-style
// parenthetical-continuation convention, so a plain "## D<n> — " anchor is
// the whole rule.
var canonicalDHeadingRe = regexp.MustCompile(`^## D(\d+) — `)

// canonicalDFHeadingRe matches a canonical finding heading in
// design/findings-*.md: "### DF<n> — <title>", with an EM DASH. This is the
// load-bearing discriminator: headings like
// "### DF8 (4th data point, 2026-05-26): ..." or
// "### DF18 (run-coverage half) — ..." are legitimate continuations/splits
// of an existing finding, not a second definition of DF8/DF18 — they don't
// match "DF<n> — " immediately after the number (there's a parenthetical,
// or a colon instead of an em dash, in between). A naive `DF\d+` grep
// reports false duplicates at DF8 and DF18; this anchor doesn't.
var canonicalDFHeadingRe = regexp.MustCompile(`^### DF(\d+) — `)

// citationDRe / citationDFRe extract rationale-ID citations from a Go
// comment's text. The \b anchors on both ends stop a longer citation number
// from matching on just its leading digits, and stop word-embedded text
// like "gocognitD30" (no boundary before the D) from matching at all.
// Because citationDRe requires a digit immediately after "D", it
// never matches inside "DF71" (the character after D is "F", not a digit) —
// so the two regexes naturally partition without needing to special-case
// each other.
var citationDRe = regexp.MustCompile(`\bD(\d+)\b`)
var citationDFRe = regexp.MustCompile(`\bDF(\d+)\b`)

// idSite is where an ID was defined or cited: "relative/path:lineNum".
type idSite = string

// scanCanonicalHeadings scans path for re, returning id (prefix + captured
// number, e.g. "D74" or "DF71") -> every site that defines it. More than
// one site for an id means it's defined twice.
func scanCanonicalHeadings(t *testing.T, root, relPath, prefix string, re *regexp.Regexp) map[string][]idSite {
	t.Helper()
	out := map[string][]idSite{}
	eachLine(t, filepath.Join(root, relPath), func(lineNum int, line string) {
		m := re.FindStringSubmatch(line)
		if m == nil {
			return
		}
		id := prefix + m[1]
		out[id] = append(out[id], fmt.Sprintf("%s:%d", relPath, lineNum))
	})
	return out
}

// mergeSites merges src into dst in place.
func mergeSites(dst, src map[string][]idSite) {
	for id, sites := range src {
		dst[id] = append(dst[id], sites...)
	}
}

// scanCitations scans every *.go file under root (as listed in goFiles,
// root-relative) for re-matching IDs inside genuine `//` comments (via
// goFileComments — never inside string literals), returning id -> every
// citing site.
func scanCitations(t *testing.T, root string, goFiles []string, prefix string, re *regexp.Regexp) map[string][]idSite {
	t.Helper()
	out := map[string][]idSite{}
	for _, rel := range goFiles {
		for _, c := range goFileComments(t, filepath.Join(root, rel)) {
			for _, m := range re.FindAllStringSubmatch(c.text, -1) {
				id := prefix + m[1]
				out[id] = append(out[id], fmt.Sprintf("%s:%d", rel, c.line))
			}
		}
	}
	return out
}

// sortedKeys returns m's keys, sorted, for deterministic failure output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// envGateRe matches a test-gate variable name and nothing else. The anchors
// matter: config and cli tests pass gate-SHAPED text as expansion fixtures
// ("${YOLOAI_TEST_NONEXISTENT}/dir"), and those are data, not gates. Used to
// validate a whole string literal.
var envGateRe = regexp.MustCompile(`^YOLOAI_TEST_[A-Z0-9_]+$`)

// envGateTokenRe finds gate names embedded in a line of make/YAML/shell, where
// they appear as `YOLOAI_TEST_X := 1` or `YOLOAI_TEST_X=1`. Unanchored, unlike
// envGateRe, because here the name is a token inside a larger line.
var envGateTokenRe = regexp.MustCompile(`YOLOAI_TEST_[A-Z0-9_]+`)

// envGateReads returns every test gate the Go file reads, keyed by name.
//
// A string literal counts as a gate only where it is an argument to os.Getenv /
// os.LookupEnv, or the value of a const/var declaration — the indirection
// testutil uses (`const integrationBackendEnv = "YOLOAI_TEST_BACKEND"`). That
// is narrower than "every matching literal" on purpose: pathutil_test.go passes
// a gate-shaped name to assert.Contains as expected error text, and an
// assertion is not a gate.
//
// Parsing rather than grepping also makes comment mentions structurally
// invisible, which is not hypothetical: integration_tart_test.go's comment
// explains the DF94 YOLOAI_TEST_TART rename, and a grep counts that dead name
// as a live read. See goFileComments' note on the same class of false positive.
func envGateReads(t *testing.T, path string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]int{}
	note := func(lit *ast.BasicLit) {
		if lit == nil || lit.Kind != token.STRING {
			return
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil || !envGateRe.MatchString(val) {
			return
		}
		out[val] = fset.Position(lit.Pos()).Line
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if isEnvReadCall(node.Fun) {
				for _, arg := range node.Args {
					lit, _ := arg.(*ast.BasicLit)
					note(lit)
				}
			}
		case *ast.ValueSpec:
			for _, v := range node.Values {
				lit, _ := v.(*ast.BasicLit)
				note(lit)
			}
		}
		return true
	})
	return out
}

// isEnvReadCall reports whether fun is os.Getenv or os.LookupEnv.
func isEnvReadCall(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "os" {
		return false
	}
	return sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv"
}

// envGateSetters returns every test gate something in-tree can actually turn
// on. Comment lines are dropped so a gate that is only *discussed* in a
// Makefile or workflow does not count as wired — "#" opens a comment in make,
// YAML and shell alike, which is the whole corpus here.
func envGateSetters(t *testing.T, root string, paths []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range paths {
		eachLine(t, filepath.Join(root, rel), func(lineNum int, line string) {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				return
			}
			for _, name := range envGateTokenRe.FindAllString(line, -1) {
				out[name] = fmt.Sprintf("%s:%d", rel, lineNum)
			}
		})
	}
	return out
}

// filterGoSuffix returns the subset of paths ending in ".go".
func filterGoSuffix(paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, ".go") {
			out = append(out, p)
		}
	}
	return out
}

// loadDHeadings scans both decision logs for canonical "## D<n> — " headings.
func loadDHeadings(t *testing.T, root string) map[string][]idSite {
	t.Helper()
	out := map[string][]idSite{}
	mergeSites(out, scanCanonicalHeadings(t, root, "docs/contributors/decisions/working-notes.md", "D", canonicalDHeadingRe))
	mergeSites(out, scanCanonicalHeadings(t, root, "docs/contributors/decisions/working-notes-archive.md", "D", canonicalDHeadingRe))
	return out
}

// loadDFHeadings scans all four findings sinks for canonical
// "### DF<n> — " headings.
func loadDFHeadings(t *testing.T, root string) map[string][]idSite {
	t.Helper()
	out := map[string][]idSite{}
	for _, rel := range []string{
		"docs/contributors/design/findings-unresolved.md",
		"docs/contributors/design/findings-resolved.md",
		"docs/contributors/design/findings-deferred.md",
		"docs/contributors/design/findings-abandoned.md",
	} {
		mergeSites(out, scanCanonicalHeadings(t, root, rel, "DF", canonicalDFHeadingRe))
	}
	return out
}

// assertResolves fails t for every citation whose id has no entry in
// headings.
func assertResolves(t *testing.T, citations, headings map[string][]idSite, headingDesc string) {
	t.Helper()
	for _, id := range sortedKeys(citations) {
		if _, ok := headings[id]; !ok {
			t.Errorf("%s cited at %v does not resolve to any %s", id, citations[id], headingDesc)
		}
	}
}

// assertNoDuplicates fails t for every id defined at more than one site in
// headings.
func assertNoDuplicates(t *testing.T, headings map[string][]idSite, corpusDesc string) {
	t.Helper()
	for _, id := range sortedKeys(headings) {
		if sites := headings[id]; len(sites) > 1 {
			t.Errorf("%s is defined %d times (%s), not once: %v — one of these needs "+
				"renumbering; any code citing %s is now ambiguous", id, len(sites), corpusDesc, sites, id)
		}
	}
}

// TestRepoHygiene_DecisionCitations_ResolveAndAreUnique is Gate B: every
// D<n>/DF<n> cited from a Go comment must resolve to a real heading, and no
// heading may define the same ID twice.
//
// False-positive rate on the current tree: 0%. Matching method: parse every
// tracked *.go file (goFileComments) and run \bD(\d+)\b / \bDF(\d+)\b
// against each genuine "//" comment's text — string-literal content is
// structurally excluded by construction (see goFileComments and
// TestRepoHygiene_GoFileComments_IgnoresStringLiterals), not merely checked
// by hand. That yields exactly 33 distinct D citations and 30 distinct DF
// citations; every one was manually checked against its source line and is
// a genuine rationale-ID reference (no hex/version/URL/prose collision
// found). An earlier line-scanning draft of this gate (strings.Index(line,
// "//")) self-hosted false positives against this very file's own
// table-driven test fixtures before goFileComments replaced it — the fixed
// approach is immune to that class of bug by construction.
func TestRepoHygiene_DecisionCitations_ResolveAndAreUnique(t *testing.T) {
	root := repoRoot(t)
	goFiles := filterGoSuffix(trackedFiles(t, root))

	dHeadings := loadDHeadings(t, root)
	dfHeadings := loadDFHeadings(t, root)
	dCitations := scanCitations(t, root, goFiles, "D", citationDRe)
	dfCitations := scanCitations(t, root, goFiles, "DF", citationDFRe)

	t.Logf("Gate B scope: %d canonical D headings, %d canonical DF headings, %d cited D ids, %d cited DF ids",
		len(dHeadings), len(dfHeadings), len(dCitations), len(dfCitations))

	t.Run("CitedDecisionsResolve", func(t *testing.T) {
		assertResolves(t, dCitations, dHeadings,
			`a "## D<n> — ..." heading in decisions/working-notes.md or working-notes-archive.md`)
	})

	t.Run("CitedFindingsResolve", func(t *testing.T) {
		assertResolves(t, dfCitations, dfHeadings,
			`a "### DF<n> — ..." heading in design/findings-*.md`)
	})

	t.Run("NoDuplicateDecisionHeadings", func(t *testing.T) {
		assertNoDuplicates(t, dHeadings, "decisions/working-notes*.md")
	})

	t.Run("NoDuplicateFindingHeadings", func(t *testing.T) {
		assertNoDuplicates(t, dfHeadings, "design/findings-*.md")
	})
}

// envGateSetterFiles returns the corpus that can turn a gate on: the Makefile,
// CI workflows, and scripts. A gate wired anywhere in here is reachable by
// somebody; a gate wired nowhere is reachable by nobody.
func envGateSetterFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		switch {
		case p == "Makefile", strings.HasSuffix(p, ".mk"),
			strings.HasPrefix(p, ".github/"), strings.HasPrefix(p, "scripts/"):
			out = append(out, p)
		}
	}
	return out
}

// TestRepoHygiene_TestGates_AreSetBySomething is Gate C: every YOLOAI_TEST_*
// gate the Go code reads must be settable by something in the tree.
//
// A scope gate nothing sets is not a skipped test, it is a deleted one that
// reports green forever — and unlike a deleted test, nothing in the diff ever
// says so. This is a grep asymmetry, which is precisely what a gate is for and
// what human review demonstrably does not catch.
//
// DF94 is the worked example. The tart lifecycle tier was gated on
// YOLOAI_TEST_TART, which nothing set: not the Makefile, not CI, not any
// script. Its near-namesake YOLOAI_TEST_TART_VM gated a busy sibling suite and
// made the tier look covered, so it self-skipped for months on the only
// platform that could run it. D112's own plan quoted the gating line in its
// keep-list and did not notice the two names differed. When it was finally
// wired on, it failed immediately, and the bugs it had been hiding included a
// shipped defect in two backends' handling of a public API.
//
// Writing this gate found two more before it ever ran: YOLOAI_TEST_SEATBELT and
// YOLOAI_TEST_APPLE, guarding the audit-C1 malicious-filter containment tests.
// Both had never run. Both passed first time. See DF99.
func TestRepoHygiene_TestGates_AreSetBySomething(t *testing.T) {
	root := repoRoot(t)
	tracked := trackedFiles(t, root)

	setters := envGateSetters(t, root, envGateSetterFiles(tracked))
	reads := map[string]string{}
	for _, rel := range filterGoSuffix(tracked) {
		for name, line := range envGateReads(t, filepath.Join(root, rel)) {
			reads[name] = fmt.Sprintf("%s:%d", rel, line)
		}
	}

	t.Logf("Gate C scope: %d gates read in Go, %d settable in Makefile/CI/scripts",
		len(reads), len(setters))

	for _, name := range sortedKeys(reads) {
		if _, ok := setters[name]; !ok {
			t.Errorf("%s is read at %s but nothing sets it — no Makefile target, CI job or "+
				"script turns it on, so the tests behind it never run and report green (DF94, DF95). "+
				"Wire it into a Makefile target, or delete the gate if the cost that justified it is gone",
				name, reads[name])
		}
	}
}

// TestRepoHygiene_EnvGateMatcher_CountsReadsNotMentions proves Gate C's
// extractor can fail in both directions that matter. It must find a gate behind
// each shape a real read takes (a bare os.Getenv literal, an os.LookupEnv, and
// the const indirection testutil uses), and it must NOT report gate-shaped text
// that is merely data: a name inside a larger expansion string, a name passed to
// an assertion, and a name mentioned in a comment. Every one of those false
// positives exists in the real tree, and a grep-based first draft reported all
// three.
func TestRepoHygiene_EnvGateMatcher_CountsReadsNotMentions(t *testing.T) {
	src := "package p\n" +
		"\n" +
		"import \"os\"\n" +
		"\n" +
		"// YOLOAI_TEST_COMMENTED was renamed; this comment is not a read.\n" +
		"const wired = \"YOLOAI_TEST_VIA_CONST\"\n" +
		"\n" +
		"func f() {\n" +
		"\t_ = os.Getenv(\"YOLOAI_TEST_VIA_GETENV\")\n" +
		"\t_, _ = os.LookupEnv(\"YOLOAI_TEST_VIA_LOOKUP\")\n" +
		"\t_ = os.Getenv(wired)\n" +
		"\t_ = expand(\"${YOLOAI_TEST_INSIDE_STRING}/dir:copy\")\n" +
		"\tassertContains(\"YOLOAI_TEST_IN_ASSERTION\")\n" +
		"}\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := sortedKeys(envGateReads(t, path))
	want := []string{"YOLOAI_TEST_VIA_CONST", "YOLOAI_TEST_VIA_GETENV", "YOLOAI_TEST_VIA_LOOKUP"}
	if !slices.Equal(got, want) {
		t.Errorf("envGateReads = %v, want %v", got, want)
	}
}

// TestRepoHygiene_EnvGateSetters_IgnoreCommentedMentions proves a gate merely
// discussed in a Makefile comment does not count as wired. Make, YAML and shell
// all open comments with "#", and every setter file is one of those. Without
// this, DF94's gate would have "passed" on the strength of the comment that
// explained it.
func TestRepoHygiene_EnvGateSetters_IgnoreCommentedMentions(t *testing.T) {
	dir := t.TempDir()
	rel := "Makefile"
	body := "## gated behind YOLOAI_TEST_ONLY_DISCUSSED=1, see the docs\n" +
		"target:\n" +
		"\tYOLOAI_TEST_REALLY_SET=1 go test ./...\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := sortedKeys(envGateSetters(t, dir, []string{rel}))
	want := []string{"YOLOAI_TEST_REALLY_SET"}
	if !slices.Equal(got, want) {
		t.Errorf("envGateSetters = %v, want %v — a commented mention must not count as wired", got, want)
	}
}

// TestRepoHygiene_HeadingMatcher_RejectsFalseDuplicates proves the DF
// discriminator can fail in both directions: it must accept a real
// canonical heading and it must reject the continuation/split shapes that a
// naive `DF\d+` grep would misreport as duplicate definitions.
func TestRepoHygiene_HeadingMatcher_RejectsFalseDuplicates(t *testing.T) {
	cases := []struct {
		name    string
		re      *regexp.Regexp
		line    string
		wantID  string
		wantHit bool
	}{
		{"canonical D heading", canonicalDHeadingRe,
			"## D74 — Lazy backend connection", "74", true},
		{"canonical DF heading", canonicalDFHeadingRe,
			"### DF73 — Leaked host-side broker process outlives its sandbox — RESOLVED", "73", true},
		{"DF continuation with parenthetical + colon is NOT a redefinition", canonicalDFHeadingRe,
			"### DF8 (4th data point, 2026-05-26): containerd-vm idle-after-prompt failed once", "", false},
		{"DF split with parenthetical before the em dash is NOT a redefinition", canonicalDFHeadingRe,
			"### DF18 (run-coverage half) — Seatbelt and Tart now have real run coverage", "", false},
		{"DF fix-note heading is NOT a redefinition", canonicalDFHeadingRe,
			"### DF8 FIX V3 LANDED 2026-05-26", "", false},
		{"D heading missing the trailing space is rejected", canonicalDHeadingRe,
			"## D74 —Lazy backend connection", "", false},
		{"D heading using a hyphen instead of an em dash is rejected", canonicalDHeadingRe,
			"## D74 - Lazy backend connection", "", false},
		{"heading at the wrong level (####) is rejected", canonicalDHeadingRe,
			"#### D74 — Lazy backend connection", "", false},
		{"DF heading matched against the D regex is rejected (namespace separation)", canonicalDHeadingRe,
			"### DF73 — Leaked host-side broker process", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := c.re.FindStringSubmatch(c.line)
			gotHit := m != nil
			if gotHit != c.wantHit {
				t.Fatalf("match(%q) hit = %v, want %v", c.line, gotHit, c.wantHit)
			}
			if gotHit && m[1] != c.wantID {
				t.Errorf("match(%q) id = %q, want %q", c.line, m[1], c.wantID)
			}
		})
	}
}

// TestRepoHygiene_CitationMatcher_ExtractsConservatively proves the
// citation matcher can fail: it must find real citations, must not cross
// the D/DF namespace boundary, and must not fire on prose. (Confinement to
// genuine comments — never string literals — is goFileComments's job; see
// TestRepoHygiene_GoFileComments_IgnoresStringLiterals for that proof.)
func TestRepoHygiene_CitationMatcher_ExtractsConservatively(t *testing.T) {
	t.Run("citationDRe and citationDFRe", func(t *testing.T) {
		cases := []struct {
			name    string
			comment string
			wantD   []string
			wantDF  []string
		}{
			{"plain D citation", "// see D62 for details", []string{"62"}, nil},
			{"two D citations, slash separated", "// (D58/D59)", []string{"58", "59"}, nil},
			{
				"D and DF together do not cross-contaminate",
				"// Reap leaked host-side broker processes whose sandbox is gone (DF71/D114).",
				[]string{"114"}, []string{"71"},
			},
			{"DF alone: D-regex must not also fire on it", "// (DF16)", nil, []string{"16"}},
			{"embedded digits with no leading boundary do not match", "// gocognitD30 is not a citation", nil, nil},
			{"D620 must not be reported as D62", "// unrelated D620 knob", []string{"620"}, nil},
			{"no citation at all", "// nothing to see here", nil, nil},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				gotD := allSubmatches(citationDRe, c.comment)
				gotDF := allSubmatches(citationDFRe, c.comment)
				if !slices.Equal(gotD, c.wantD) {
					t.Errorf("citationDRe on %q = %v, want %v", c.comment, gotD, c.wantD)
				}
				if !slices.Equal(gotDF, c.wantDF) {
					t.Errorf("citationDFRe on %q = %v, want %v", c.comment, gotDF, c.wantDF)
				}
			})
		}
	})
}

// allSubmatches returns every captured group 1 from re.FindAllStringSubmatch.
func allSubmatches(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// ---------------------------------------------------------------------------
// Gate C: no //nolint directive suppresses the complexity gate, and
// .golangci.yml still pins its thresholds.
// ---------------------------------------------------------------------------

// complexityLinters are the three names development-principles.md §10
// forbids loosening: extract a function, don't suppress the gate that
// caught it.
var complexityLinters = map[string]bool{"cyclop": true, "gocognit": true, "gocyclo": true}

// nolintComplexityName returns the complexity-linter name a //nolint
// directive on line suppresses, and true, if any. A bare `//nolint` (no
// `:name,...` list) is not "naming" a linter within the meaning of this
// gate — it's a different, broader problem this test doesn't police.
func nolintComplexityName(line string) (string, bool) {
	idx := strings.Index(line, "//nolint")
	if idx == -1 {
		return "", false
	}
	rest := line[idx+len("//nolint"):]
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	rest = rest[1:]
	if sp := strings.IndexAny(rest, " \t"); sp != -1 {
		rest = rest[:sp]
	}
	for _, name := range strings.Split(rest, ",") {
		if complexityLinters[name] {
			return name, true
		}
	}
	return "", false
}

// TestRepoHygiene_NoComplexitySuppression_AllTrackedFiles is Gate C part 1:
// no tracked *.go file may contain a //nolint directive naming
// cyclop/gocognit/gocyclo. Verified at authoring time: 0 of ~1152 //nolint
// directives suppress any of the three — this should be GREEN today.
//
// Scans genuine comments only (goFileComments), not raw text lines: this
// file's own nolintComplexityName test fixtures below embed
// "//nolint:...gocognit..." as string-literal data, and a raw line scan
// self-hosted a false positive against them before this switched to the
// parser. See TestRepoHygiene_GoFileComments_IgnoresStringLiterals.
func TestRepoHygiene_NoComplexitySuppression_AllTrackedFiles(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)

	var violations []string
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		for _, c := range goFileComments(t, filepath.Join(root, rel)) {
			if name, ok := nolintComplexityName(c.text); ok {
				violations = append(violations, fmt.Sprintf("%s:%d suppresses %s", rel, c.line, name))
			}
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		var b strings.Builder
		b.WriteString("//nolint directives suppress the complexity gate " +
			"(development-principles.md §10 forbids this — extract a function instead):\n")
		for _, v := range violations {
			b.WriteString("  " + v + "\n")
		}
		t.Error(b.String())
	}
}

// TestRepoHygiene_ComplexityThresholds_PinnedInGolangciYML is Gate C part
// 2: raising the limit is the other way to defeat the gate instead of
// extracting a function, so the thresholds themselves must stay pinned to
// their literal values.
func TestRepoHygiene_ComplexityThresholds_PinnedInGolangciYML(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".golangci.yml")) //nolint:gosec // G304: fixed repo-root-relative path, not attacker input
	if err != nil {
		t.Fatalf("read .golangci.yml: %v", err)
	}
	if err := checkComplexityThresholds(string(data)); err != nil {
		t.Error(err)
	}
}

// cyclopThresholdRe / gocognitThresholdRe anchor each threshold to its own
// settings block so a max-complexity/min-complexity elsewhere in the file
// (there isn't one today, but nothing stops it) can't be mistaken for the
// pinned value.
var cyclopThresholdRe = regexp.MustCompile(`(?s)cyclop:\s*\n\s*max-complexity:\s*15\b`)
var gocognitThresholdRe = regexp.MustCompile(`(?s)gocognit:\s*\n\s*min-complexity:\s*20\b`)

// checkComplexityThresholds is the matcher Gate C part 2 runs against
// .golangci.yml's contents; factored out so it can be table-tested against
// literal strings without touching the real file.
func checkComplexityThresholds(yaml string) error {
	var problems []string
	if !cyclopThresholdRe.MatchString(yaml) {
		problems = append(problems, `cyclop.max-complexity is not pinned to 15`)
	}
	if !gocognitThresholdRe.MatchString(yaml) {
		problems = append(problems, `gocognit.min-complexity is not pinned to 20`)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf(".golangci.yml complexity thresholds changed: %s", strings.Join(problems, "; "))
}

// TestRepoHygiene_ComplexitySuppressionMatcher_RejectsBadDirectives proves
// both halves of Gate C can fail: a //nolint that does name a complexity
// linter, and a .golangci.yml whose thresholds have drifted from the pinned
// values.
func TestRepoHygiene_ComplexitySuppressionMatcher_RejectsBadDirectives(t *testing.T) {
	t.Run("nolintComplexityName", func(t *testing.T) {
		cases := []struct {
			name     string
			line     string
			wantName string
			wantHit  bool
		}{
			{"suppresses cyclop directly", `func big() { //nolint:cyclop`, "cyclop", true},
			{"suppresses gocognit among others", `} //nolint:gosec,gocognit // long but simple`, "gocognit", true},
			{"suppresses gocyclo", `//nolint:gocyclo`, "gocyclo", true},
			{"unrelated nolint is allowed", `data, err := os.ReadFile(p) //nolint:gosec // G304: path derived from sandboxes dir`, "", false},
			{"bare nolint (no linter list) is not \"naming\" one", `x := f() //nolint`, "", false},
			{"linter name embedded in a longer word is not a match", `//nolint:gocognitwhatever`, "", false},
			{"no nolint directive on the line at all", `func small() {}`, "", false},
			{"prose mentioning the word is not a directive", `// keeping this under the cyclop limit`, "", false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				name, hit := nolintComplexityName(c.line)
				if hit != c.wantHit || name != c.wantName {
					t.Errorf("nolintComplexityName(%q) = (%q, %v), want (%q, %v)", c.line, name, hit, c.wantName, c.wantHit)
				}
			})
		}
	})

	t.Run("checkComplexityThresholds", func(t *testing.T) {
		cases := []struct {
			name    string
			yaml    string
			wantErr bool
		}{
			{"both pinned correctly", "settings:\n  cyclop:\n    max-complexity: 15\n  gocognit:\n    min-complexity: 20\n", false},
			{"cyclop loosened to 30", "settings:\n  cyclop:\n    max-complexity: 30\n  gocognit:\n    min-complexity: 20\n", true},
			{"gocognit loosened", "settings:\n  cyclop:\n    max-complexity: 15\n  gocognit:\n    min-complexity: 999\n", true},
			{"cyclop section removed entirely", "settings:\n  gocognit:\n    min-complexity: 20\n", true},
			{"both missing", "settings:\n  errcheck: {}\n", true},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				err := checkComplexityThresholds(c.yaml)
				if (err != nil) != c.wantErr {
					t.Errorf("checkComplexityThresholds(%q) error = %v, wantErr %v", c.yaml, err, c.wantErr)
				}
			})
		}
	})
}
