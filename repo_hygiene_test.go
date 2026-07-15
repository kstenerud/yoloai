// ABOUTME: Repo-hygiene gates that run under plain `go test ./...` (no build
// ABOUTME: tags), so `make check` enforces them on every PR: ABOUTME headers
// ABOUTME: (markdown.md), D/DF rationale-ID citations resolve and don't
// ABOUTME: collide, and no //nolint suppresses the complexity gate.

package yoloai_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// This file enforces three standing claims that nothing else in the build
// checks:
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
//
// Deliberately NOT gated here: markdown.md also requires ABOUTME headers on
// docs/contributors/**/*.md. 122 of those files are currently missing one —
// a known, tracked bulk-add task, not a per-PR regression. Gating it here
// would make this test permanently red for a gap a separate task owns; once
// that sweep lands, extend Gate A to cover docs/contributors/**/*.md too.
//
// Also deliberately NOT gated: ABOUTME line width. markdown.md's ABOUTME
// section states no width rule (the "keep under 80 chars" text lives only
// inside the illustrative example block, not as a stated requirement), and
// 330+ existing ABOUTME lines already exceed 80 columns. Adding a width
// check here would invent a rule the standard doesn't make and fail on
// pre-existing, compliant files.

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
func TestRepoHygiene_ABOUTMEHeaders_AllTrackedFilesCompliant(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)

	counted := map[string]int{"go": 0, "python": 0, "shell": 0}
	var missing []string
	for _, rel := range files {
		cat := aboutmeCategory(rel)
		if cat == "" {
			continue
		}
		counted[cat]++
		abs := filepath.Join(root, rel)
		if !hasABOUTMEHeader(firstLines(t, abs, 6)) {
			missing = append(missing, rel)
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
// comment's text. \b on both ends keeps "D620" from matching as "D62", and
// keeps prose like "gocognitD30" (no word boundary before D) from matching
// at all. Because citationDRe requires a digit immediately after "D", it
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

// commentText returns the "//..." suffix of line, or "", false if line has
// no "//". Conservative by construction: everything before the first "//"
// is ignored, so an ID appearing only in code (not in any comment) is never
// picked up. Checked against the current tree: no tracked Go file has a
// string literal containing "//" followed by a D<n>/DF<n>-shaped token
// ahead of its real trailing comment, so this simple split has a 0%
// false-positive rate today; if that ever changes, tighten this to a real
// token scanner instead of widening an allowlist.
func commentText(line string) (string, bool) {
	idx := strings.Index(line, "//")
	if idx == -1 {
		return "", false
	}
	return line[idx:], true
}

// scanCitations scans every *.go file under root (as listed in goFiles,
// root-relative) for re-matching IDs inside `//` comments, returning id ->
// every citing site.
func scanCitations(t *testing.T, root string, goFiles []string, prefix string, re *regexp.Regexp) map[string][]idSite {
	t.Helper()
	out := map[string][]idSite{}
	for _, rel := range goFiles {
		eachLine(t, filepath.Join(root, rel), func(lineNum int, line string) {
			comment, ok := commentText(line)
			if !ok {
				return
			}
			for _, m := range re.FindAllStringSubmatch(comment, -1) {
				id := prefix + m[1]
				out[id] = append(out[id], fmt.Sprintf("%s:%d", rel, lineNum))
			}
		})
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
// False-positive rate on the current tree: 0%. Matching method: for every
// tracked *.go file, take the "//"-suffix of each line (see commentText)
// and run \bD(\d+)\b / \bDF(\d+)\b against it. That yields exactly 33
// distinct D citations and 30 distinct DF citations; every one was manually
// checked against its source line and is a genuine rationale-ID reference
// (no hex/version/URL/prose collision found). If a future change to this
// codebase's comment style produces a collision, narrow commentText (or the
// regexes) rather than adding an ignore-list.
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
// the D/DF namespace boundary, must not fire on prose, and must not look
// outside a `//` comment.
func TestRepoHygiene_CitationMatcher_ExtractsConservatively(t *testing.T) {
	t.Run("commentText", func(t *testing.T) {
		cases := []struct {
			name string
			line string
			want string
			ok   bool
		}{
			{"line with trailing comment", `x := 1 // see D62`, `// see D62`, true},
			{"pure comment line", `// D62/D63 refinement`, `// D62/D63 refinement`, true},
			{"no comment marker at all", `return errD62`, "", false},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				got, ok := commentText(c.line)
				if ok != c.ok || got != c.want {
					t.Errorf("commentText(%q) = (%q, %v), want (%q, %v)", c.line, got, ok, c.want, c.ok)
				}
			})
		}
	})

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
				if !equalStringSlices(gotD, c.wantD) {
					t.Errorf("citationDRe on %q = %v, want %v", c.comment, gotD, c.wantD)
				}
				if !equalStringSlices(gotDF, c.wantDF) {
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

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
func TestRepoHygiene_NoComplexitySuppression_AllTrackedFiles(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)

	var violations []string
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		eachLine(t, filepath.Join(root, rel), func(lineNum int, line string) {
			if name, ok := nolintComplexityName(line); ok {
				violations = append(violations, fmt.Sprintf("%s:%d suppresses %s", rel, lineNum, name))
			}
		})
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
