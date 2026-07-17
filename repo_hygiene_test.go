// ABOUTME: Repo-hygiene gates that run under plain `go test ./...` (no build
// ABOUTME: tags), so `make check` enforces on every PR the standing claims that
// ABOUTME: no linter can express. Each gate below states what it enforces and
// ABOUTME: what went wrong without it; grep `func TestRepoHygiene_` for the set.

package yoloai_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/build/constraint"
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
	"time"
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
//  1. Every Go, runtime/monitor Python, scripts/ shell, and live
//     docs/contributors Markdown file carries an ABOUTME header, in the form
//     its language requires, per docs/contributors/standards/markdown.md.
//     Without this, a new file can silently skip the convention forever —
//     nothing else notices; the doc's "100% compliant" claim would rot the
//     moment someone forgets.
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
//  5. No `//go:build integration` file passes `io.Discard` to a setup call.
//     Without this, a step that pulls ~30 GB or builds an image reports nothing
//     while it runs, and a stall becomes indistinguishable from a wedge — see
//     DF97, where it cost a ~35 minute misdiagnosis on Tart and reduced a real
//     docker build failure to an exit code with no cause.
//  6. Every Go path and every symbol named by docs/contributors/architecture/**
//     still exists in the tree. Without this, the tier whose whole job is
//     describing the code as it is now drifts silently and confidently: at D124
//     it documented a mode retired two releases earlier, a package that has
//     never existed, a file that is not on disk, and ~30 absent functions —
//     green the entire time, because nothing executes prose.
//  7. Every relative markdown link (`[text](path.md)`) in a live tracked *.md
//     file resolves to a real file. D124 gated this for architecture/ alone and
//     said so out loud: "No other tier has that backstop." It didn't: a
//     plan-archival move left 41 links across decisions/ and design/ pointing at
//     files that had moved out from under them. docs/contributors/archive/** is
//     exempt — frozen history, not a live tier; see isLiveMarkdownDoc.
//
// (docs/contributors/**/*.md USED to be listed here as deliberately not gated,
// on the grounds that the bulk-add was "a known, tracked bulk-add task". It was
// known and it was not tracked: no issue, no plan, no finding, in a repo that
// files everything. The exemption had become the only record of the gap it
// described, which is a backlog of one that nothing would ever drain. The sweep
// landed and the gate now covers them; the archive stays out, and that is a
// standing rule with a reason, not a deferral.)
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
// ABOUTME headers (docs/contributors/standards/markdown.md "Required" list)
// on Go, runtime/monitor Python, and scripts/ shell files.
// ---------------------------------------------------------------------------

// aboutmeBlock returns the ABOUTME header lines in head (the file's first N
// lines) for the given category, or nil if there is no correctly-formed header.
//
// It matches the required form, not merely the string "ABOUTME:", because the
// form is now the rule: a bare `ABOUTME:` or an `<!-- ABOUTME: -->` in a
// Markdown doc is a header in the wrong shape, and a presence-only test would
// pass both. Those were 38 files each before markdown.md settled the question.
//
// Returning the block rather than a bool is what lets the caller width-check
// every line of it. That matters for Markdown specifically: only the first line
// carries the marker, so a "lines containing ABOUTME:" width check would leave
// every continuation line unbounded.
func aboutmeBlock(head []string, category string) []string {
	marker, everyLine := aboutmeHeaderForm(category)
	if marker == "" {
		return nil
	}
	start := slices.IndexFunc(head, func(l string) bool {
		return strings.HasPrefix(strings.TrimSpace(l), marker)
	})
	if start < 0 {
		return nil
	}
	if everyLine {
		return slices.DeleteFunc(slices.Clone(head), func(l string) bool {
			return !strings.HasPrefix(strings.TrimSpace(l), marker)
		})
	}
	// Markdown: the marker opens a blockquote and the block is the rest of it.
	end := start
	for end < len(head) && strings.HasPrefix(strings.TrimSpace(head[end]), ">") {
		end++
	}
	return head[start:end]
}

// aboutmeCategory classifies a tracked path into one of the gated buckets, or
// "" if the file isn't in scope for the ABOUTME gate. The buckets exist because
// the required header form differs per bucket (see aboutmeHeaderForm).
func aboutmeCategory(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasPrefix(path, "runtime/monitor/") && strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasPrefix(path, "scripts/") && strings.HasSuffix(path, ".sh"):
		return "shell"
	case isGatedContributorDoc(path):
		return "markdown"
	default:
		return ""
	}
}

// isGatedContributorDoc reports whether path is a contributor Markdown doc that
// must carry an ABOUTME (markdown.md → Required / Exempt).
//
// The archive is the carve-out, and it is a real rule rather than a backlog
// dodge: docs/contributors/archive/ is frozen, its contents are to be read as
// aged and possibly rotted, and conforming a frozen document to a current
// convention implies someone vouched for it. Its own README is the exception —
// an index is live navigation, so it is gated like any other doc.
//
// docs/ above contributors/ is out of scope entirely: the GUIDE, ROADMAP and
// BREAKING-CHANGES are content destinations, not source context.
func isGatedContributorDoc(path string) bool {
	if !strings.HasPrefix(path, "docs/contributors/") || !strings.HasSuffix(path, ".md") {
		return false
	}
	if strings.HasPrefix(path, "docs/contributors/archive/") {
		return path == "docs/contributors/archive/README.md"
	}
	return true
}

// aboutmeHeaderForm returns the marker a bucket's ABOUTME must start each line
// with, and whether every line must carry it.
//
// Markdown differs from the comment languages on purpose, and shallowly: it has
// no comment syntax, and it joins consecutive lines into one paragraph, so a
// per-line "ABOUTME:" prefix renders as a run-on with the marker repeated
// mid-sentence. The blockquote marks the block once and lets the prose flow.
// markdown.md carries the full reasoning; this function is the enforcement.
func aboutmeHeaderForm(category string) (marker string, everyLine bool) {
	switch category {
	case "go":
		return "// ABOUTME:", true
	case "python", "shell":
		return "# ABOUTME:", true
	case "markdown":
		return "> **ABOUTME:**", false
	default:
		return "", false
	}
}

// aboutmeMaxCols is the ABOUTME line-width limit from
// docs/contributors/standards/markdown.md, comment marker included.
//
// 100, not 80 (D117). markdown.md said 80, but only inside an example block
// rather than as a rule, and nothing enforced it: 351 lines across 256 files had
// drifted past. Gating at 80 would have meant reflowing a quarter of the repo to
// satisfy a number nobody had ever applied. 100 is what the code already does —
// exactly four lines exceeded it, and those were reflowed, not grandfathered.
//
// Those numbers are past tense on purpose: they are what the tree held when D117
// decided the threshold, so no later edit can falsify them. That is the only kind
// of count D121 leaves standing outside a gate.
const aboutmeMaxCols = 100

// TestRepoHygiene_ABOUTMEHeaders_AllTrackedFilesCompliant is the ABOUTME gate: every
// tracked *.go, runtime/monitor/*.py, and scripts/*.sh file must carry an
// ABOUTME: line in its first 6 lines, within aboutmeMaxCols.
//
// A failure here is a real gap (a new file that skipped the convention), not a
// flaky check; there is no allowlist to add to. The gate logs its own live scope
// per bucket, which is where to look for the counts this comment used to state
// and get wrong: "Go 580/580, scripts/*.sh 4/4" was written here at authoring
// time and was 585 and 5 within days (D121).
func TestRepoHygiene_ABOUTMEHeaders_AllTrackedFilesCompliant(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)

	counted := map[string]int{}
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
		block := aboutmeBlock(head, cat)
		if block == nil {
			marker, _ := aboutmeHeaderForm(cat)
			missing = append(missing, fmt.Sprintf("%s (wants %q)", rel, marker))
			continue
		}
		for _, line := range block {
			// RuneCountInString, not len(): len() counts BYTES, and these comments
			// use em-dashes freely (3 bytes each). A byte count reports a 99-column
			// line as 101 and rejects correct code — it fired on
			// runtime/containerd/exec.go the first time this ran. The limit is columns.
			if cols := utf8.RuneCountInString(line); cols > aboutmeMaxCols {
				tooWide = append(tooWide, fmt.Sprintf("%s (%d cols)", rel, cols))
			}
		}
	}

	t.Logf("ABOUTME gate scope: go=%d python(runtime/monitor)=%d shell(scripts/)=%d markdown(docs/contributors)=%d",
		counted["go"], counted["python"], counted["shell"], counted["markdown"])

	if len(missing) > 0 {
		sort.Strings(missing)
		var b strings.Builder
		b.WriteString("files with no ABOUTME header in the required form, in their first 6 lines\n")
		b.WriteString("(docs/contributors/standards/markdown.md → \"ABOUTME header (source files)\"):\n")
		for _, m := range missing {
			b.WriteString("  " + m + "\n")
		}
		b.WriteString("Fix: add the header at the top of the file, in the form named above. A\n")
		b.WriteString("Markdown doc needs the blockquote form; a bare \"ABOUTME:\" renders as a\n")
		b.WriteString("run-on paragraph and an HTML comment renders as nothing.\n")
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
// half of the ABOUTME gate can fail, and pins the byte-vs-rune trap it was born from:
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

// TestRepoHygiene_ABOUTMEMatcher_RejectsBadHeaders proves the ABOUTME gate's matcher
// can fail: it is a pure table test against literal strings, independent of
// repo content, so a gate that has silently stopped checking anything would
// still be caught here.
func TestRepoHygiene_ABOUTMEMatcher_RejectsBadHeaders(t *testing.T) {
	cases := []struct {
		name string
		cat  string
		want int // lines in the returned block; 0 means "no valid header"
		body []string
	}{
		{"go header present", "go", 1, []string{
			"// ABOUTME: does the thing.", "", "package foo",
		}},
		{"go header spanning lines collects them all", "go", 2, []string{
			"// ABOUTME: does the thing,", "// ABOUTME: and the other thing.", "", "package foo",
		}},
		{"python header present", "python", 1, []string{
			"# ABOUTME: does the other thing.", "", "import os",
		}},
		{"missing entirely", "go", 0, []string{
			"package foo", "", "func main() {}",
		}},
		{"header beyond the 6-line window is not seen by caller", "go", 0, []string{
			// firstLines(path, 6) would never hand this slice to the matcher
			// in the real gate; included here to document that the matcher
			// itself only looks at what it's given, the window is the
			// caller's job.
			"package foo", "", "", "", "", "",
		}},
		{"empty file", "go", 0, nil},
		{"similar but wrong marker (typo) is rejected", "go", 0, []string{
			"// ABOUT: does the thing.",
		}},

		// Markdown: the form IS the rule. Both rejected shapes below existed in
		// the tree in roughly equal numbers before markdown.md settled it, so a
		// presence-only matcher passed all three and enforced nothing.
		{"markdown blockquote header, continuation lines included", "markdown", 2, []string{
			"> **ABOUTME:** what this doc is for,", "> continued here.", "", "# Title",
		}},
		{"markdown bare ABOUTME is rejected — it renders as a run-on", "markdown", 0, []string{
			"ABOUTME: what this doc is for.", "", "# Title",
		}},
		{"markdown HTML comment is rejected — it renders as nothing", "markdown", 0, []string{
			"<!-- ABOUTME: what this doc is for. -->", "", "# Title",
		}},
		{"markdown blockquote that is not an ABOUTME is not a header", "markdown", 0, []string{
			"> Note: this doc is deprecated.", "", "# Title",
		}},
		{"the blockquote block stops at the end of the quote", "markdown", 1, []string{
			"> **ABOUTME:** one line only.", "", "# Title", "> a later quote is not the header",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := aboutmeBlock(c.body, c.cat)
			if len(got) != c.want {
				t.Errorf("aboutmeBlock(%q, %q) = %q (%d lines), want %d lines",
					c.body, c.cat, got, len(got), c.want)
			}
		})
	}
}

// TestRepoHygiene_ABOUTMEScope_ExemptsTheFrozenArchive pins the one carve-out in
// the ABOUTME gate's Markdown scope, in both directions.
//
// The archive is exempt because it is frozen and its contents are to be read as
// aged and possibly rotted; conforming them would imply someone vouched for them
// (../archive/README.md). Its own README is not exempt — an index is live
// navigation. Everything above docs/contributors/ is out of scope entirely.
func TestRepoHygiene_ABOUTMEScope_ExemptsTheFrozenArchive(t *testing.T) {
	cases := map[string]bool{
		"docs/contributors/README.md":                     true,
		"docs/contributors/standards/markdown.md":         true,
		"docs/contributors/design/plans/some-plan.md":     true,
		"docs/contributors/archive/README.md":             true,  // the index stays live
		"docs/contributors/archive/plans/old-plan.md":     false, // frozen
		"docs/contributors/archive/old/phases/PHASE_0.md": false,
		"docs/GUIDE.md":              false, // a content destination
		"docs/integrators/README.md": false,
		"README.md":                  false,
		"docs/contributors/design/plans/notes.txt": false, // not Markdown
	}
	for path, want := range cases {
		if got := isGatedContributorDoc(path); got != want {
			t.Errorf("isGatedContributorDoc(%q) = %v, want %v", path, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Rationale-ID citations: every D<n>/DF<n> cited from a Go comment resolves,
// and no ID is defined twice.
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

// TestRepoHygiene_DecisionCitations_ResolveAndAreUnique is the citation gate: every
// D<n>/DF<n> cited from a Go comment must resolve to a real heading, and no
// heading may define the same ID twice.
//
// Matching method: parse every tracked *.go file (goFileComments) and run
// \bD(\d+)\b / \bDF(\d+)\b against each genuine "//" comment's text —
// string-literal content is structurally excluded by construction (see
// goFileComments and TestRepoHygiene_GoFileComments_IgnoresStringLiterals),
// not merely checked by hand. Every citation it extracted was manually
// checked against its source line when this gate was written, and all were
// genuine rationale-ID references — no hex/version/URL/prose collision. The
// live counts are in the gate's own scope log; the tally that stood here
// ("exactly 33 distinct D citations and 30 distinct DF citations") had drifted
// to 36 and 35 within days, which is D121 in the file that gates for it. An
// earlier line-scanning draft of this gate (strings.Index(line,
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

	t.Logf("citation gate scope: %d canonical D headings, %d canonical DF headings, %d cited D ids, %d cited DF ids",
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

// TestRepoHygiene_TestGates_AreSetBySomething is the test-gate liveness gate: every YOLOAI_TEST_*
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

	t.Logf("test-gate liveness scope: %d gates read in Go, %d settable in Makefile/CI/scripts",
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

// TestRepoHygiene_EnvGateMatcher_CountsReadsNotMentions proves the test-gate liveness gate's
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
// Complexity: no //nolint directive suppresses cyclop/gocognit/gocyclo, and
// .golangci.yml still pins their thresholds.
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

// TestRepoHygiene_NoComplexitySuppression_AllTrackedFiles is the complexity gate's first half:
// no tracked *.go file may contain a //nolint directive naming
// cyclop/gocognit/gocyclo. The repo has many //nolint directives and none of
// them suppress any of the three — which is the gate's whole claim, so it is
// enforced rather than tallied here (D121).
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

// TestRepoHygiene_ComplexityThresholds_PinnedInGolangciYML is the complexity gate's second half
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

// checkComplexityThresholds is the matcher the complexity gate's second half runs against
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
// both halves of the complexity gate can fail: a //nolint that does name a complexity
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

// ---------------------------------------------------------------------------
// The discard gate: a real-backend test must not discard the output of a
// chatty, long-running setup call (DF97).
//
// EnsureSetup pulls base images and builds them. `io.Discard` there hid two
// separate failures in one session: the Tart tier swallowed the base-image
// banner ("This is a one-time download (~30 GB)"), so a ~35 minute download
// presented as a wedged VM and a bare 10 minute timeout panic; the docker
// bootstrap swallowed a build's stderr, reducing a real failure to "docker build
// exited with code 1" with no cause. A slow step and a hung step are
// indistinguishable once the output is gone, which is exactly when it is needed.
//
// Why this is a gate and not a forbidigo rule: the wanted rule is
// argument-positional. Most io.Discard sites in the tree are production
// `if out == nil { out = io.Discard }` — the correct implementation of a
// documented `Default: io.Discard` contract (client.go, system.go, engine.go,
// launch.go, ptybridge, the --json writers). Banning the expression buys a crop
// of reflexive nolints and teaches the habit that defeats the rule. forbidigo
// matches the expression and cannot see the call it sits in; this file already
// parses Go and can see both.
// ---------------------------------------------------------------------------

// setupOutputSinks are the setup callees whose output argument must not be
// discarded under a real backend. Matched on the selector name alone: this gate
// has no type information, and a false positive here can only be a call named
// EnsureSetup/Setup that takes an io.Discard it does not print to, which the
// matcher test pins as acceptable.
var setupOutputSinks = []string{"EnsureSetup", "Setup"}

// requiresIntegrationTag reports whether f is built only when the integration
// tag is set. That tag is the discriminator this whole gate rests on:
// integration-tagged files drive real backends, where EnsureSetup pulls, builds
// and can hang; untagged ones drive fakes. internal/orchestrator/engine_test.go
// passes io.Discard to EnsureSetup repeatedly and every one is correct — the
// fake runtime emits nothing, so there is no output to lose. A gate that flags
// those is dead on arrival.
func requiresIntegrationTag(f *ast.File) bool {
	for _, group := range f.Comments {
		// Build constraints precede the package clause; a //go:build-shaped line
		// anywhere after it is just a comment.
		if group.Pos() > f.Package {
			return false
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return false
			}
			return constraintRequiresTag(expr, "integration")
		}
	}
	return false
}

// constraintRequiresTag reports whether expr is unsatisfiable without tag. The
// three shapes in the tree are "integration", "integration && linux" and
// "integration && !linux", but spelling this out structurally rather than
// string-matching the first line is what keeps a fourth shape from silently
// falling out of scope — a gate's corpus going quiet is the failure mode the test-gate liveness gate
// exists to catch, and it applies to this gate too.
func constraintRequiresTag(expr constraint.Expr, tag string) bool {
	switch x := expr.(type) {
	case *constraint.TagExpr:
		return x.Tag == tag
	case *constraint.AndExpr:
		return constraintRequiresTag(x.X, tag) || constraintRequiresTag(x.Y, tag)
	case *constraint.OrExpr:
		// An alternative only requires the tag if every branch does.
		return constraintRequiresTag(x.X, tag) && constraintRequiresTag(x.Y, tag)
	case *constraint.NotExpr:
		// "!integration" marks a file as explicitly NOT an integration file.
		return false
	}
	return false
}

// discardedSetupOutput returns the lines where path passes io.Discard to a setup
// callee, plus whether path was in scope at all. A file that does not require
// the integration tag is never in scope, so its correct io.Discard calls are
// invisible here rather than allowlisted.
func discardedSetupOutput(t *testing.T, path string) (lines []int, inScope bool) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if !requiresIntegrationTag(f) {
		return nil, false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !slices.Contains(setupOutputSinks, sel.Sel.Name) {
			return true
		}
		for _, arg := range call.Args {
			if isIODiscard(arg) {
				lines = append(lines, fset.Position(arg.Pos()).Line)
			}
		}
		return true
	})
	return lines, true
}

// isIODiscard reports whether e is the expression io.Discard. An assignment
// (`out = io.Discard`) is structurally not a call argument, so the nil-default
// contract shape never reaches this.
func isIODiscard(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "io" && sel.Sel.Name == "Discard"
}

// TestRepoHygiene_IntegrationSetupOutput_IsNotDiscarded is the discard gate: no
// integration-tagged file may discard a setup call's output.
//
// The replacement exists, which is what makes the ban fair: testutil.LogWriter(t)
// forwards to t.Log, and `go test` prints the log of a failed or verbose test, so
// a stalled step explains itself. Where there is no *testing.T to log through (a
// sync.Once, a TestMain), the answer is still not io.Discard: capture into a
// bytes.Buffer and attach it to the error — warmDockerBase in
// integration_helpers_test.go is the worked example, and it exists precisely
// because "docker build exited with code 1" with the cause discarded is what made
// DF97.
func TestRepoHygiene_IntegrationSetupOutput_IsNotDiscarded(t *testing.T) {
	root := repoRoot(t)
	goFiles := filterGoSuffix(trackedFiles(t, root))

	scoped := 0
	for _, rel := range goFiles {
		lines, inScope := discardedSetupOutput(t, filepath.Join(root, rel))
		if !inScope {
			continue
		}
		scoped++
		for _, line := range lines {
			t.Errorf("%s:%d discards the output of a setup call in an integration-tagged file. "+
				"A real backend pulls, builds and can hang here, and a discarded stall is "+
				"indistinguishable from a wedge (DF97). Pass testutil.LogWriter(t) instead; "+
				"with no *testing.T in scope, capture into a bytes.Buffer and attach it to the "+
				"error, as warmDockerBase does.", rel, line)
		}
	}

	t.Logf("discard gate scope: %d integration-tagged Go files of %d tracked", scoped, len(goFiles))
	if scoped == 0 {
		t.Error("the discard gate matched no integration-tagged files at all — the corpus went quiet, " +
			"which means the gate is not checking anything (compare the test-gate liveness gate's DF94)")
	}
}

// TestRepoHygiene_DiscardMatcher_ScopesToIntegrationTaggedFiles proves the discard gate's
// matcher can fail in both directions that matter. It must catch the banned shape
// under each build-tag spelling the tree actually uses, and it must NOT flag the
// io.Discard shapes that are correct: the same setup call in an untagged unit
// test (engine_test.go's five), the nil-default contract assignment, and a
// discard handed to something that is not a setup call.
func TestRepoHygiene_DiscardMatcher_ScopesToIntegrationTaggedFiles(t *testing.T) {
	const body = "\npackage p\n" +
		"\n" +
		"import \"io\"\n" +
		"\n" +
		"func f(mgr M, ctx C, out io.Writer) {\n" +
		"\t_ = mgr.EnsureSetup(ctx, io.Discard)\n" +
		"}\n"

	cases := []struct {
		name      string
		src       string
		wantLines []int
		wantScope bool
	}{
		{
			name:      "bare integration tag is in scope and flags",
			src:       "//go:build integration\n" + body,
			wantLines: []int{8},
			wantScope: true,
		},
		{
			name:      "integration && linux is still an integration file",
			src:       "//go:build integration && linux\n" + body,
			wantLines: []int{8},
			wantScope: true,
		},
		{
			name:      "integration && !linux is still an integration file",
			src:       "//go:build integration && !linux\n" + body,
			wantLines: []int{8},
			wantScope: true,
		},
		{
			name:      "an untagged unit test is out of scope entirely",
			src:       body,
			wantScope: false,
		},
		{
			name:      "!integration is not an integration file",
			src:       "//go:build !integration\n" + body,
			wantScope: false,
		},
		{
			name: "the nil-default contract shape is not a discarded argument",
			src: "//go:build integration\n" +
				"\npackage p\n\nimport \"io\"\n\n" +
				"func g(out io.Writer) io.Writer {\n" +
				"\tif out == nil {\n" +
				"\t\tout = io.Discard\n" +
				"\t}\n" +
				"\treturn out\n" +
				"}\n",
			wantScope: true,
		},
		{
			name: "a discard passed to a non-setup callee is not this gate's business",
			src: "//go:build integration\n" +
				"\npackage p\n\nimport (\n\t\"fmt\"\n\t\"io\"\n)\n\n" +
				"func h() {\n" +
				"\tfmt.Fprintln(io.Discard, \"noise\")\n" +
				"}\n",
			wantScope: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(path, []byte(c.src), 0600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			lines, inScope := discardedSetupOutput(t, path)
			if inScope != c.wantScope {
				t.Errorf("inScope = %v, want %v", inScope, c.wantScope)
			}
			if !slices.Equal(lines, c.wantLines) {
				t.Errorf("lines = %v, want %v", lines, c.wantLines)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Plan status: every plan declares one, and a finished plan doesn't live here.
//
// The archive rule was stated in four places and followed in none —
// design/plans/ held plans reading "IMPLEMENTED", "DONE" and "Phases 1 & 2
// COMPLETE" while its own index called itself "Unimplemented Features". Prose
// asking people to remember to move a file cannot fire: the author of a finished
// plan is never prompted, and nothing downstream notices. So the token carries
// it, and marking a plan finished is what trips the gate.
//
// Each token earns its place by answering a question someone actually asks:
// "what haven't we fleshed out?" (UNSPECIFIED), "what can I build?" (PLANNED),
// "what's underway?" (IN-PROGRESS), "did we ever do or consider X?" (the
// archive). A DEFERRED token was proposed and dropped for failing that test —
// "what's deferred?" is "what's planned but parked", and the parking reason is
// prose in the plan, not a state. Nothing in the tree had ever carried the
// Trigger: line the deferred convention requires, so it was a distinction never
// practiced.
//
// IN-PROGRESS exists because six live plans would otherwise have to lie:
// tamper-resistant-network-isolation shipped for docker and defers
// containerd/Kata and macOS; mandatory-infra-test-policy is Linux-verified with
// macOS pending. A four-token vocabulary without it archives those and buries
// the pending work — the plan is usually the only record of it.
//
// What this deliberately does NOT do: read archive/. The archive is frozen, and
// gating a frozen file means conforming it (archive/README.md). Forbidding the
// finished tokens *here* forces the move without the gate ever having an opinion
// about a file that has already left.
// ---------------------------------------------------------------------------

// planStatusRe matches "**Status:** TOKEN" at the start of a line, where TOKEN is
// followed by an em-dash separator or the end of the line.
//
// The separator is load-bearing, not punctuation. Without it this pattern reads
// the tree's legacy free-form lines as tokens, and it reads them wrong in the one
// direction that costs something: "**Status:** IMPLEMENTED for the container
// backends" tokenizes as IMPLEMENTED, and the gate would then order a plan into
// the archive on the strength of a sentence saying only part of it shipped.
// "**Status:** Active on the module-split branch" degrades the same way, to a
// bare "A". Requiring the separator makes every legacy line fail as undeclared,
// which is the safe direction: it demands an explicit classification rather than
// inferring one from a sentence's first word.
var planStatusRe = regexp.MustCompile(`(?m)^- \*\*Status:\*\* ([A-Z][A-Z-]*)(?: —|\s*$)`)

// planDependsRe matches the "Depends on" entry of the same metadata list. The
// value is other plans' filenames, or an em dash for nothing.
//
// This field is gateable, which is the whole reason it exists rather than living
// in a roadmap table: a named plan must be a real file here, so the reference
// cannot rot, and archiving a plan that something still depends on fails loudly
// instead of leaving a dangling name behind (DF103). It answers "what can I start
// now?" — a plan whose dependencies have all left for the archive is unblocked.
var planDependsRe = regexp.MustCompile(`(?m)^- \*\*Depends on:\*\* (.+)$`)

// livePlanStatuses are the tokens a plan in design/plans/ may carry.
var livePlanStatuses = []string{"UNSPECIFIED", "PLANNED", "IN-PROGRESS"}

// retiredPlanStatuses mean the plan's work is over. Valid tokens — just not here.
var retiredPlanStatuses = []string{"IMPLEMENTED", "ABANDONED"}

// planStatusTokens returns every canonical status token declared in a plan.
//
// Every, not the first, because "the first one wins" is how a plan ends up with
// two answers and no way to tell which is live. store-workload-split carried a
// top line reading "Scoped ... gated decision pending" while a section below it
// read "C1 + C2 + C3 all DONE" — it had shipped, and the line a reader trusts
// first was the stale one. A gate that silently took the first match would
// inherit that bug rather than report it.
func planStatusTokens(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // G304: path comes from git ls-files under repoRoot
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, m := range planStatusRe.FindAllSubmatch(body, -1) {
		out = append(out, string(m[1]))
	}
	return out
}

// livePlanFiles returns the tracked plan documents, excluding the index.
func livePlanFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, "docs/contributors/design/plans/") && strings.HasSuffix(p, ".md") &&
			p != "docs/contributors/design/plans/README.md" {
			out = append(out, p)
		}
	}
	return out
}

// planDependencies returns the plan filenames a plan declares it depends on.
// A bare em dash means none. Returns ok=false when the field is absent entirely,
// which is a different failure from declaring no dependencies.
func planDependencies(t *testing.T, path string) (deps []string, ok bool) {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // G304: path comes from git ls-files under repoRoot
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := planDependsRe.FindSubmatch(body)
	if m == nil {
		return nil, false
	}
	raw := strings.TrimSpace(string(m[1]))
	if raw == "—" || raw == "-" || raw == "none" {
		return nil, true
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(strings.Trim(strings.TrimSpace(part), "`[]()"))
		if part != "" {
			deps = append(deps, part)
		}
	}
	return deps, true
}

// TestRepoHygiene_PlanDependencies_Resolve is the other half of the plan-status
// gate: a declared dependency must name a plan that is actually here.
//
// This is what makes the field worth carrying. An ungated metadata field is a
// claim that ages in silence — the reason "Effort" and "Layer" were considered
// and left out. A dependency, by contrast, is checkable: the name either
// resolves to a live plan or it does not. That kills two failures at once. A
// dangling name (typo, or a plan renamed under it) fails here rather than
// misleading a reader into thinking work is blocked on something that no longer
// exists. And archiving a plan that another still depends on fails loudly, which
// is the case a human review would miss — the dependent plan is untouched by
// that PR, so nothing draws the eye to it.
//
// An archived plan is finished and therefore blocks nothing, so pointing at one
// is an error rather than a special case: rephrase the dependency or drop it.
func TestRepoHygiene_PlanDependencies_Resolve(t *testing.T) {
	root := repoRoot(t)
	plans := livePlanFiles(trackedFiles(t, root))
	live := map[string]bool{}
	for _, rel := range plans {
		live[filepath.Base(rel)] = true
	}

	declared := 0
	for _, rel := range plans {
		deps, ok := planDependencies(t, filepath.Join(root, rel))
		if !ok {
			t.Errorf("%s declares no \"- **Depends on:**\" entry. Every plan carries one, under "+
				"its Status; use an em dash if it depends on nothing. Without it, \"what can I "+
				"start now?\" has no answer that a grep can give.", rel)
			continue
		}
		declared++
		for _, d := range deps {
			if live[d] {
				continue
			}
			hint := "no plan by that name is here"
			if _, err := os.Stat(filepath.Join(root, "docs/contributors/archive/plans", d)); err == nil {
				hint = "that plan is archived, so its work is finished and it blocks nothing — " +
					"drop the dependency or say what actually remains"
			}
			t.Errorf("%s depends on %q, but %s. A dependency names a live plan file in "+
				"design/plans/ (see design/plans/README.md).", rel, d, hint)
		}
	}
	t.Logf("plan-dependency gate scope: %d plans declaring dependencies", declared)
}

// TestRepoHygiene_PlanStatus_IsDeclaredAndLive is the plan-status gate.
func TestRepoHygiene_PlanStatus_IsDeclaredAndLive(t *testing.T) {
	root := repoRoot(t)
	plans := livePlanFiles(trackedFiles(t, root))

	t.Logf("plan-status gate scope: %d live plans", len(plans))
	if len(plans) == 0 {
		t.Fatal("no plans under design/plans/ — the gate's corpus went quiet, which means it is " +
			"checking nothing (the failure mode DF94 documents)")
	}

	for _, rel := range plans {
		toks := planStatusTokens(t, filepath.Join(root, rel))
		if len(toks) > 1 {
			t.Errorf("%s declares its status %d times (%v). One plan, one status: a second "+
				"declaration is a second authoritative location and nothing keeps them in step "+
				"(D121). Per-phase progress belongs in that phase's prose, not in another "+
				"\"**Status:** TOKEN\" line.", rel, len(toks), toks)
			continue
		}
		tok := ""
		if len(toks) == 1 {
			tok = toks[0]
		}
		switch {
		case tok == "":
			t.Errorf("%s declares no status. Add \"**Status:** TOKEN — prose\" under the title. "+
				"TOKEN is one of %v for a plan that lives here; %v means the work is over and the "+
				"plan belongs in archive/plans/ — see design/plans/README.md",
				rel, livePlanStatuses, retiredPlanStatuses)
		case slices.Contains(retiredPlanStatuses, tok):
			t.Errorf("%s is marked %s but still lives in design/plans/. A finished plan is "+
				"archaeology: move it whole to docs/contributors/archive/plans/ in this same "+
				"change (AGENTS.md rule 8). If work remains, it is IN-PROGRESS, not %s.",
				rel, tok, tok)
		case !slices.Contains(livePlanStatuses, tok):
			t.Errorf("%s declares unknown status %q. The vocabulary is exactly %v here, plus %v "+
				"for a plan on its way to the archive.", rel, tok, livePlanStatuses, retiredPlanStatuses)
		}
	}
}

// TestRepoHygiene_PlanStatusMatcher_RejectsBadTokens proves the matcher fails in
// both directions. The prose after the token stays free — that is where "what
// remains" lives, and constraining it would push the detail out of the doc.
func TestRepoHygiene_PlanStatusMatcher_RejectsBadTokens(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"canonical unspecified", "# T\n\n- **Status:** UNSPECIFIED — an idea; no design yet.\n", "UNSPECIFIED"},
		{"canonical planned", "# T\n\n- **Status:** PLANNED — designed 2026-07-01, not started.\n", "PLANNED"},
		{"canonical in-progress", "# T\n\n- **Status:** IN-PROGRESS — docker shipped; macOS pending.\n", "IN-PROGRESS"},
		{"retired token is still extracted, so the gate can reject it", "# T\n\n- **Status:** IMPLEMENTED — done 2026-07-06.\n", "IMPLEMENTED"},
		{"a bare token with no prose is fine", "# T\n\n- **Status:** PLANNED\n", "PLANNED"},
		{"no status line at all", "# T\n\nSome prose about the plan.\n", ""},
		{"the old bolded shape is not a token", "# T\n\n- Status: **IMPLEMENTED for the container backends**\n", ""},

		// The dangerous class: right prefix, legacy prose after it. Reading a
		// token out of these is worse than reading none — the sentence usually
		// contradicts the word it opens with.
		{"legacy prose is NOT a token, even when it opens with one", "# T\n\n- **Status:** IMPLEMENTED for the container backends (docker/podman), 2026-06-29.\n", ""},
		{"a capitalised sentence does not degrade to its first letter", "# T\n\n- **Status:** Active on the module-split branch, cut from main.\n", ""},
		{"nor does a hyphenated one", "# T\n\n- **Status:** Scoping-stage draft, not started.\n", ""},
		{"lower-case is not a token", "# T\n\n- **Status:** implemented — done.\n", ""},
		{"the token must open the line, not appear mid-sentence", "# T\n\n- The **Status:** PLANNED marker goes below.\n", ""},
		{"prose after the token is unconstrained", "# T\n\n- **Status:** IN-PROGRESS — Phases 1 & 2 of 4 on `main`; next is egress (D105).\n", "IN-PROGRESS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.md")
			if err := os.WriteFile(path, []byte(c.body), 0600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got := ""
			if toks := planStatusTokens(t, path); len(toks) > 0 {
				got = toks[0]
			}
			if got != c.want {
				t.Errorf("planStatusTokens first = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRepoHygiene_PlanStatusDup_IsDetected proves the extractor sees a second
// declaration rather than silently taking the first — the shape that let
// store-workload-split read "gated decision pending" at the top while a section
// below it said the work had all landed.
func TestRepoHygiene_PlanStatusDup_IsDetected(t *testing.T) {
	body := "# T\n\n- **Status:** PLANNED — scoped, gated decision pending.\n\n" +
		"## Phase C\n\n- **Status:** IMPLEMENTED — C1, C2 and C3 all landed.\n"
	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := planStatusTokens(t, path)
	want := []string{"PLANNED", "IMPLEMENTED"}
	if !slices.Equal(got, want) {
		t.Errorf("planStatusTokens = %v, want %v — both declarations must be seen, or the gate "+
			"inherits the ambiguity instead of reporting it", got, want)
	}
}

// TestRepoHygiene_PlanDependsMatcher_ParsesTheField proves the dependency parser
// distinguishes the three states that matter: no field at all (a gate error),
// an explicit "nothing" (fine), and a real list (each name must resolve).
func TestRepoHygiene_PlanDependsMatcher_ParsesTheField(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
		ok   bool
	}{
		{"absent entirely", "# T\n\n- **Status:** PLANNED — x.\n", nil, false},
		{"em dash means nothing blocks it", "# T\n\n- **Depends on:** —\n", nil, true},
		{"a single plan", "# T\n\n- **Depends on:** store-workload-split.md\n", []string{"store-workload-split.md"}, true},
		{"several, comma separated", "# T\n\n- **Depends on:** a-plan.md, b-plan.md\n", []string{"a-plan.md", "b-plan.md"}, true},
		{"backticked names are unwrapped", "# T\n\n- **Depends on:** `a-plan.md`\n", []string{"a-plan.md"}, true},
		{"not a list item is not the field", "# T\n\n**Depends on:** a-plan.md\n", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.md")
			if err := os.WriteFile(path, []byte(c.body), 0600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got, ok := planDependencies(t, path)
			if ok != c.ok || !slices.Equal(got, c.want) {
				t.Errorf("planDependencies = (%v, %v), want (%v, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}

// TestRepoHygiene_PlanStatusScope_ExcludesTheIndexAndTheArchive pins the gate's
// corpus: the index is not a plan, and the archive is frozen.
func TestRepoHygiene_PlanStatusScope_ExcludesTheIndexAndTheArchive(t *testing.T) {
	got := livePlanFiles([]string{
		"docs/contributors/design/plans/some-plan.md",
		"docs/contributors/design/plans/README.md",
		"docs/contributors/archive/plans/old-plan.md",
		"docs/contributors/design/research/a-spike.md",
		"docs/contributors/design/plans/notes.txt",
	})
	want := []string{"docs/contributors/design/plans/some-plan.md"}
	if !slices.Equal(got, want) {
		t.Errorf("livePlanFiles = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Architecture-doc reference gate
// ---------------------------------------------------------------------------

// architectureDocs returns the tracked Markdown under docs/contributors/architecture/.
// That tier's whole job is describing the code as it is now, which is exactly
// the claim that rots without something checking it.
func architectureDocs(files []string) []string {
	var out []string
	for _, p := range files {
		if strings.HasPrefix(p, "docs/contributors/architecture/") && strings.HasSuffix(p, ".md") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// collectGoIdents adds every identifier appearing in f to names — declarations,
// but also parameters, locals, and use sites.
//
// The question this gate asks is "does the code still know this name?", not "is
// it exported surface": a map doc legitimately names a struct field
// (SupportedIsolationModes, which is a field and not the method a doc once
// claimed), a parameter (workDir), and an unexported local. Demanding a
// top-level declaration failed all three while catching nothing extra — every
// real defect it found (ApplyOverlay, GenerateMultiDiff, NewGitCmd) is a name
// the tree does not contain at all, in any position.
//
// Comments and string literals are not identifiers, so they still cannot vouch
// for a name — which is the whole reason this parses instead of grepping.
func collectGoIdents(f *ast.File, names map[string]bool) {
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			names[id.Name] = true
		}
		return true
	})
}

// goIdentSet is the oracle the doc gate checks against: every identifier the Go
// tree contains.
//
// It parses rather than greps, for the same reason the ABOUTME gate does: a
// grep for a bare name also matches that name inside a comment or a string
// literal, so a symbol that survives only in the prose describing it would
// vouch for itself. Parsing asks the one question that matters — does the code
// still know this name?
func goIdentSet(t *testing.T, root string, files []string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	fset := token.NewFileSet()
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			continue // unparseable fixture — the ABOUTME gate owns that complaint, not this one
		}
		collectGoIdents(f, names)
	}
	return names
}

// docProseNouns are words that appear inside fenced blocks but are pictures,
// not code: platform and product names in ASCII diagrams. Nothing in the Go
// tree will ever declare them, so the gate would demand the impossible.
//
// Kept tiny and explicit on purpose. This is the gate's one concession, and it
// is a list of proper nouns — if it ever starts accumulating things that look
// like Go identifiers, the concession has become a loophole and the entry
// belongs back in the doc as a real name instead.
var docProseNouns = map[string]bool{
	"macOS":    true,
	"yoloAI":   true,
	"OpenCode": true,
	"OpenAI":   true,
	"GitHub":   true,
}

var (
	// docGoPathRe matches a Go file path written anywhere in a doc.
	docGoPathRe = regexp.MustCompile(`\b[A-Za-z0-9_][A-Za-z0-9_./-]*\.go\b`)
	// docCallRe matches a backticked `Name()` / `pkg.Name()` call reference.
	docCallRe = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_.]*)\\(\\)`")
	// docIdentRe matches an identifier, optionally package- or type-qualified.
	docIdentRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)
	// docCamelRe is the "this is a symbol, not prose" test: an inner lower→upper
	// transition. It keeps GenerateDiff and envsetup.SeedSandbox, and drops
	// English, flags (--no-start), and SHOUTING_CONSTANTS.
	docCamelRe = regexp.MustCompile(`[a-z][A-Z]`)
)

// baseIdent strips any package or receiver qualifier: store.SaveEnvironment and
// Workdir.Apply are checked as SaveEnvironment and Apply. The qualifier is not
// checked — a package rename is a path question, which docGoPathRefs covers.
func baseIdent(tok string) string {
	if i := strings.LastIndex(tok, "."); i >= 0 {
		return tok[i+1:]
	}
	return tok
}

// docGoPathRefs returns every *.go path a doc names.
func docGoPathRefs(body string) []string {
	return uniqueSorted(docGoPathRe.FindAllString(body, -1))
}

// docSymbolRefs returns the symbol names a doc commits to: identifiers inside
// fenced blocks (where the call chains live) plus backticked `Name()` calls
// anywhere (where the package tables live).
//
// Scoping to those two forms — rather than all prose — is what lets this gate
// run with no allowlist. Free prose says "yoloAI" and "macOS"; fenced diagrams
// and `Name()` spans only ever mean code.
func docSymbolRefs(body string) []string {
	var toks []string
	for _, m := range docCallRe.FindAllStringSubmatch(body, -1) {
		toks = append(toks, baseIdent(m[1]))
	}
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			continue
		}
		for _, tok := range docIdentRe.FindAllString(line, -1) {
			if docCamelRe.MatchString(tok) {
				toks = append(toks, baseIdent(tok))
			}
		}
	}
	return uniqueSorted(toks)
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// TestRepoHygiene_ArchitectureDocRefs_Resolve is the architecture-doc reference
// gate: every Go path and every symbol the architecture tier names must still
// exist.
//
// Without it, that tier drifts silently and confidently. When this gate was
// written, data-flows.md documented a `provision/` package that has never
// existed, a squash-by-default apply that had become opt-in, and an entire
// "Overlay Mount Flow" section for a mode retired two releases earlier;
// code-map.md listed `copyflow/apply_overlay.go`, a file that is not there, and
// 20-odd functions that are not either. `make check` was green throughout —
// nothing executes prose (D124).
//
// The gate checks names, not narrative. It cannot know whether a described
// ordering is still true, which is the other half of why the tier points at
// source comments for rationale instead of restating them: a pointer can be
// verified, a paraphrase cannot.
func TestRepoHygiene_ArchitectureDocRefs_Resolve(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)
	docs := architectureDocs(files)
	if len(docs) == 0 {
		t.Fatal("no docs under docs/contributors/architecture/ — the gate's corpus went quiet, " +
			"which means it is checking nothing (the failure mode DF94 documents)")
	}
	declared := goIdentSet(t, root, files)
	tracked := map[string]bool{}
	for _, p := range files {
		tracked[p] = true
	}

	paths, syms := 0, 0
	for _, rel := range docs {
		body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // G304: path from git ls-files under repoRoot
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		text := string(body)
		paths += checkDocGoPaths(t, rel, text, tracked)
		syms += checkDocSymbols(t, rel, text, declared)
	}
	t.Logf("architecture-doc ref gate scope: %d docs, %d Go-path refs, %d symbol refs", len(docs), paths, syms)
}

// checkDocGoPaths reports every Go path rel names that no tracked file matches,
// and returns how many it checked.
func checkDocGoPaths(t *testing.T, rel, text string, tracked map[string]bool) int {
	t.Helper()
	n := 0
	for _, p := range docGoPathRefs(text) {
		// A `_`-prefixed name is a suffix pattern being described ("a _linux.go
		// file"), not a path: the Go toolchain ignores files that start with _,
		// so no such file can exist to point at.
		if strings.HasPrefix(filepath.Base(p), "_") {
			continue
		}
		n++
		if !trackedPathMatches(tracked, p) {
			t.Errorf("%s names Go file %q, which does not exist. A doc that points at a moved "+
				"or deleted file sends the reader somewhere empty; fix the path or drop the "+
				"claim.", rel, p)
		}
	}
	return n
}

// checkDocSymbols reports every symbol rel names that the Go tree does not
// contain, and returns how many it checked.
func checkDocSymbols(t *testing.T, rel, text string, declared map[string]bool) int {
	t.Helper()
	n := 0
	for _, s := range docSymbolRefs(text) {
		if docProseNouns[s] {
			continue
		}
		n++
		if !declared[s] {
			t.Errorf("%s names %q, which the Go tree does not declare (checked case-sensitively: "+
				"an export or de-export counts as a rename). Point at what the code calls it "+
				"now, or delete the claim.", rel, s)
		}
	}
	return n
}

// trackedPathMatches accepts an exact tracked path or a suffix of one, because
// the docs abbreviate (`copyflow/diff.go` for a repo-root package). A suffix
// match still fails for a file that is gone, which is what the gate is for.
func trackedPathMatches(tracked map[string]bool, p string) bool {
	if tracked[p] {
		return true
	}
	for got := range tracked {
		if strings.HasSuffix(got, "/"+p) {
			return true
		}
	}
	return false
}

// TestRepoHygiene_ArchitectureDocRefMatcher_ExtractsConservatively pins what the
// matcher does and does not claim, because a matcher that quietly extracted
// nothing would report a clean tier forever.
func TestRepoHygiene_ArchitectureDocRefMatcher_ExtractsConservatively(t *testing.T) {
	body := "" +
		"Prose naming yoloAI and macOS must not be extracted, nor must a `plain` span.\n" +
		"But `GenerateMultiDiff()` and `copyflow.ApplyOverlay()` are call claims.\n" +
		"```\n" +
		"NewDiffCmd (internal/cli/workflow/diff.go)\n" +
		"  → envsetup.SeedSandbox → CAP_SYS_ADMIN --no-start git add -A\n" +
		"```\n" +
		"Outside the fence, camelCase like someLocalThing is prose, not a claim.\n"

	gotSyms := docSymbolRefs(body)
	wantSyms := []string{"ApplyOverlay", "GenerateMultiDiff", "NewDiffCmd", "SeedSandbox"}
	if !slices.Equal(gotSyms, wantSyms) {
		t.Errorf("docSymbolRefs = %v, want %v", gotSyms, wantSyms)
	}

	gotPaths := docGoPathRefs(body)
	wantPaths := []string{"internal/cli/workflow/diff.go"}
	if !slices.Equal(gotPaths, wantPaths) {
		t.Errorf("docGoPathRefs = %v, want %v", gotPaths, wantPaths)
	}
}

// TestRepoHygiene_ArchitectureDocOracle_IgnoresCommentsAndStrings pins the
// oracle's one load-bearing property: a name that survives only in a comment or
// a string literal must not vouch for a doc that cites it.
//
// This is the whole reason the oracle parses instead of grepping. A retired
// symbol is usually still *discussed* in the code — "ApplyOverlay was removed
// in D109" is exactly the kind of comment a careful codebase leaves behind — so
// a grep-based oracle would have called the retired name live and reported the
// architecture tier clean while it documented a deleted feature.
func TestRepoHygiene_ArchitectureDocOracle_IgnoresCommentsAndStrings(t *testing.T) {
	src := `package p
// ApplyOverlay was retired in D109; this comment must not vouch for it.
type Descriptor struct { SupportedIsolationModes []string }
type Backend interface { Inspect(ctx string) error }
const ApplyModeCommits = "commits"
var note = "GenerateMultiDiff lives on only in this string"
func Exported(workDir string) error { sandboxState := 1; _ = sandboxState; return nil }
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	names := map[string]bool{}
	collectGoIdents(f, names)

	// Real identifiers vouch — including a struct field, an interface method, a
	// parameter, and a local, all of which a map doc legitimately names.
	for _, want := range []string{
		"Descriptor", "SupportedIsolationModes", "Backend", "Inspect",
		"ApplyModeCommits", "Exported", "workDir", "sandboxState",
	} {
		if !names[want] {
			t.Errorf("collectGoIdents dropped %q, which the code really does contain", want)
		}
	}
	// Prose does not.
	for _, reject := range []string{"ApplyOverlay", "GenerateMultiDiff"} {
		if names[reject] {
			t.Errorf("collectGoIdents admitted %q, which appears only in a comment or string — "+
				"a retired symbol that the code merely talks about must not vouch for a doc", reject)
		}
	}
}

var (
	// docSectionRe matches a package section heading. The first backticked token
	// ending in `/` is the section's package; anything after it (a "(façade)"
	// note, sibling leaves) is prose the gate ignores.
	docSectionRe = regexp.MustCompile("^#+ +`([A-Za-z0-9_./-]+/)`")
	// docFileRowRe matches a table row whose first cell is a bare filename, which
	// is how code-map.md names a file *within* the section's package.
	docFileRowRe = regexp.MustCompile("^\\| +`([A-Za-z0-9_.-]+\\.(?:go|py|sh|js|ts))` +\\|")
)

// docSectionFileRefs returns (section-package, filename) for every table row
// that names a file inside a package section.
//
// This is the section-scoped half of the doc gate. It exists because the
// whole-tree path check cannot see it: a row under `internal/workspace/` naming
// `apply.go` passes there on the strength of `copyflow/apply.go`, which is a
// different file in a different package. Reading the row against its own
// heading is the only way to ask the question the reader is actually asking.
func docSectionFileRefs(body string) [][2]string {
	var out [][2]string
	section := ""
	for _, line := range strings.Split(body, "\n") {
		if m := docSectionRe.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if strings.HasPrefix(line, "#") {
			// A non-package heading (a type, a file, a prose section) ends the
			// previous package's scope rather than inheriting it.
			section = ""
			continue
		}
		if section == "" {
			continue
		}
		if m := docFileRowRe.FindStringSubmatch(line); m != nil {
			out = append(out, [2]string{section, m[1]})
		}
	}
	return out
}

// TestRepoHygiene_ArchitectureDocSections_NameRealFiles is the section-scoped
// path gate (DF107).
//
// Its sibling, TestRepoHygiene_ArchitectureDocRefs_Resolve, suffix-matches
// against the whole tree, because the docs abbreviate. That tolerance has a
// hole: a *moved* file still resolves somewhere. When this gate was written,
// code-map.md's `internal/workspace/` section listed `apply.go` and `diff.go`
// — both had moved to `copyflow/` — its `internal/config/` section listed
// `errors.go` (it is `yoerrors/`), and its `create/` section claimed
// `context.go` (it is `internal/envsetup/`). All three passed the whole-tree
// check and were found by hand.
//
// A section heading must name a real package for the same reason: `extension/`
// pointed at a directory that does not exist at that path (the package is
// `internal/cli/extension/`), so every row under it was scoped to nothing.
func TestRepoHygiene_ArchitectureDocSections_NameRealFiles(t *testing.T) {
	root := repoRoot(t)
	docs := architectureDocs(trackedFiles(t, root))

	sections, rows := 0, 0
	seen := map[string]bool{}
	for _, rel := range docs {
		body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // G304: path from git ls-files under repoRoot
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, ref := range docSectionFileRefs(string(body)) {
			pkg, file := ref[0], ref[1]
			if !seen[pkg] {
				seen[pkg] = true
				sections++
				if fi, statErr := os.Stat(filepath.Join(root, pkg)); statErr != nil || !fi.IsDir() {
					t.Errorf("%s has a section for package %q, which is not a directory. Name the "+
						"real path (the repo spells these out — `internal/cli/`, not `cli/`) so a "+
						"reader can follow it and this gate can scope its rows.", rel, pkg)
				}
			}
			rows++
			if _, statErr := os.Stat(filepath.Join(root, pkg, file)); statErr != nil {
				t.Errorf("%s lists %q under section %q, but %s does not exist. The file may have "+
					"moved to another package — in which case the row belongs there, not here.",
					rel, file, pkg, pkg+file)
			}
		}
	}
	t.Logf("architecture-doc section gate scope: %d package sections, %d file rows", sections, rows)
}

// TestRepoHygiene_ArchitectureDocSectionMatcher_ScopesRowsToTheirHeading pins
// the matcher: rows belong to the package heading above them, a non-package
// heading ends that scope rather than inheriting it, and a type heading is not
// a package.
func TestRepoHygiene_ArchitectureDocSectionMatcher_ScopesRowsToTheirHeading(t *testing.T) {
	body := "" +
		"### `internal/workspace/` (façade)\n" +
		"| File | Purpose |\n" +
		"|------|---------|\n" +
		"| `copy.go` | copies things. |\n" +
		"| `apply.go` | moved away; must still be attributed to workspace. |\n" +
		"### `copyflow/`\n" +
		"| `diff.go` | diffs things. |\n" +
		"### `yoloai.Client`\n" +
		"| `stray.go` | a type section is not a package; this row has no scope. |\n" +
		"## Prose\n" +
		"| `alsostray.go` | likewise. |\n"

	got := docSectionFileRefs(body)
	want := [][2]string{
		{"internal/workspace/", "copy.go"},
		{"internal/workspace/", "apply.go"},
		{"copyflow/", "diff.go"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("docSectionFileRefs = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Repo-wide markdown link-resolution gate
// ---------------------------------------------------------------------------

// isLiveMarkdownDoc reports whether path is a tracked Markdown file this gate
// should hold to account.
//
// docs/contributors/archive/** is the one exemption, and for the same reason
// isGatedContributorDoc carves it out of the ABOUTME gate: it is frozen
// history. A link inside it pointed at something real on the day the page was
// written; the plan-archival move that broke 96 of its links didn't make that
// page wrong about its own past, and repointing them would make the archive
// assert a location it never actually lived at when the words were written.
// AGENTS.md rule 2 draws the same line for name sweeps — archive/ is exempt
// there too, and for the identical reason. Every other tracked *.md is prose a
// reader follows today, so a link in it is a claim about today's tree.
func isLiveMarkdownDoc(path string) bool {
	return strings.HasSuffix(path, ".md") && !strings.HasPrefix(path, "docs/contributors/archive/")
}

var (
	// mdLinkRe matches a markdown link's target: `[text](target)`.
	mdLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	// mdCodeSpanRe matches an inline code span, stripped before link-matching so
	// a literal example like `` `[text](url)` `` or a backticked regex whose
	// character class looks link-shaped (`` `(?:[._-]...)` ``) is not mistaken
	// for a real link — the same reason docSymbolRefs only reads fenced blocks
	// and docCallRe requires backticks: code syntax being discussed is not code
	// syntax being used.
	mdCodeSpanRe = regexp.MustCompile("`[^`]*`")
)

// mdRelativeLinkTargets returns every relative-link target body names, with
// any #anchor stripped and http(s)/mailto links excluded. Fenced code blocks
// and inline code spans are stripped first, mirroring docSymbolRefs's fence
// handling — a link shown as a syntax example is prose about markdown, not a
// claim the doc is making.
func mdRelativeLinkTargets(body string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		clean := mdCodeSpanRe.ReplaceAllString(line, "")
		for _, m := range mdLinkRe.FindAllStringSubmatch(clean, -1) {
			if target := mdRelativeLinkTarget(m[1]); target != "" {
				out = append(out, target)
			}
		}
	}
	return out
}

// mdRelativeLinkTarget normalizes a single raw link target, or returns "" if
// it names something this gate does not check: an external URL, a mailto:, a
// same-page anchor, or a bare anchor-only target.
func mdRelativeLinkTarget(raw string) string {
	target := strings.TrimSpace(raw)
	if target == "" || strings.HasPrefix(target, "#") {
		return ""
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
		strings.HasPrefix(target, "mailto:") {
		return ""
	}
	if i := strings.Index(target, "#"); i >= 0 {
		target = target[:i]
	}
	if target == "" || strings.ContainsAny(target, " \t") {
		// A relative path never carries whitespace; anything that does is
		// prose an over-eager match swept up, not a link the author meant to
		// be followed (e.g. a malformed reference-style stand-in).
		return ""
	}
	return target
}

// TestRepoHygiene_MarkdownLinks_Resolve is the repo-wide link gate: every
// relative link in a live tracked *.md file must resolve to a real file.
//
// D124 built the architecture/-only version of this and said, out loud, "No
// other tier has that backstop." That was true the day it was written and
// stopped being true the next time a doc moved: an archival sweep (commits
// cf52b743/19b11c00) relocated ~10 finished plans into archive/plans/ without
// repointing the links that named their old home, breaking 41 links across
// decisions/working-notes.md and design/**. Nothing failed, because nothing
// checked — the same failure mode D124 exists to close, one tier over.
//
// This gate does not follow #anchors (a moved heading inside a page that still
// exists is a much smaller miss than a page that no longer does), and it does
// not check archive/ (isLiveMarkdownDoc explains why). Everything else with a
// relative markdown link is in scope.
func TestRepoHygiene_MarkdownLinks_Resolve(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)
	var docs []string
	for _, p := range files {
		if isLiveMarkdownDoc(p) {
			docs = append(docs, p)
		}
	}
	sort.Strings(docs)
	if len(docs) == 0 {
		t.Fatal("no live tracked *.md files — the gate's corpus went quiet, which means it is " +
			"checking nothing (the failure mode DF94 documents)")
	}

	links := 0
	for _, rel := range docs {
		body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // G304: path from git ls-files under repoRoot
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		dir := filepath.Dir(rel)
		for _, target := range mdRelativeLinkTargets(string(body)) {
			links++
			resolved := filepath.ToSlash(filepath.Join(dir, target))
			if _, statErr := os.Stat(filepath.Join(root, resolved)); statErr != nil {
				t.Errorf("%s links to %q, which resolves to %q — that file does not exist. Repoint "+
					"the link to where the target actually lives, or drop the link.", rel, target, resolved)
			}
		}
	}
	t.Logf("markdown link gate scope: %d live docs, %d relative links", len(docs), links)
}

// TestRepoHygiene_MarkdownLinkMatcher_IgnoresCodeSpansAndFences pins the
// matcher's one load-bearing property: a link-shaped span that is markdown
// syntax being *discussed* (an inline-code example, a regex character class
// that happens to contain balanced brackets and parens) must not be read as a
// link the doc is *making*. Without this, standards.md's own “ `[text](url)`
// “ example and a backticked regex like “ `(?:[._-]...)` “ both misparse as
// broken links, and a gate that cries wolf on its own documentation gets
// disabled rather than trusted.
func TestRepoHygiene_MarkdownLinkMatcher_IgnoresCodeSpansAndFences(t *testing.T) {
	body := "" +
		"Standard Markdown `[text](url)` form is not a link to check.\n" +
		"Nor is a backticked regex like `^[A-Za-z0-9]+(?:[._-](?:[A-Za-z0-9]+))*$`.\n" +
		"But [real link](docs/target.md) is, and so is [with anchor](docs/target.md#section).\n" +
		"```\n" +
		"[fenced](docs/should-not-be-checked.md)\n" +
		"```\n" +
		"[external](https://example.com/x.md) and [mail](mailto:a@b.com) are skipped,\n" +
		"and so is a bare [anchor-only](#section).\n"

	got := mdRelativeLinkTargets(body)
	want := []string{"docs/target.md", "docs/target.md"}
	if !slices.Equal(got, want) {
		t.Errorf("mdRelativeLinkTargets = %v, want %v", got, want)
	}
}

// deprecationEntryRe matches a register entry's heading under "## Register".
var deprecationEntryRe = regexp.MustCompile(`(?m)^### (.+)$`)

// deprecationIncurredRe matches the "- **Incurred:** YYYY-MM-DD" datum — the
// FACT, recovered from git, that makes a due date defensible rather than invented.
var deprecationIncurredRe = regexp.MustCompile(`- \*\*Incurred:\*\* (\d{4}-\d{2}-\d{2})`)

// deprecationDueRe matches the "**Due:** YYYY-MM-DD" datum — the DECISION, chosen
// per entry from the register's recommended grace periods, because the population
// a compatibility waits on differs (stale data dirs vs people editing scripts).
var deprecationDueRe = regexp.MustCompile(`\*\*Due:\*\* (\d{4}-\d{2}-\d{2})`)

// TestRepoHygiene_DeprecationsAreDated enforces the FORMAT of the deprecation
// register (D127): every entry carries a parseable date it was incurred and a
// stated way to retire it.
//
// It deliberately does NOT fail on an overdue entry. The settling period is a
// nag, not a gate — the owner decides when a deprecation converts into a
// retirement, and a red bar lands that decision on whoever next runs `make
// check` rather than on the person who can make it (D127, rejected (a)). What is
// gated is only the thing a human cannot recover later: an entry added without
// the date it was incurred is worthless the moment `git blame` on the register
// stops matching the code's own history.
func TestRepoHygiene_DeprecationsAreDated(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "contributors", "deprecations.md")
	data, err := os.ReadFile(path) //nolint:gosec // G304: fixed repo-relative path
	if err != nil {
		t.Fatalf("read the deprecation register: %v", err)
	}

	// Only the Register section holds entries; "## Not deprecations" below it
	// lists permanents that must NOT carry a date.
	body := string(data)
	start := strings.Index(body, "\n## Register\n")
	if start < 0 {
		t.Fatal("deprecations.md has no '## Register' section — the gate's corpus moved")
	}
	register := body[start:]
	if end := strings.Index(register, "\n## Not deprecations\n"); end >= 0 {
		register = register[:end]
	}

	entries := deprecationEntryRe.FindAllStringSubmatch(register, -1)
	t.Logf("deprecation register scope: %d entries", len(entries))
	if len(entries) == 0 {
		t.Fatal("the deprecation register is empty — either every compatibility mechanism was " +
			"retired (celebrate, then delete this gate) or the gate is checking nothing, which " +
			"is the failure mode DF94 documents")
	}

	// Split the register into per-entry blocks so each entry's fields are checked
	// against that entry, not against the section as a whole.
	blocks := deprecationEntryRe.Split(register, -1)[1:] // [0] is the pre-heading preamble
	for i, m := range entries {
		title, block := m[1], blocks[i]
		t.Run(title, func(t *testing.T) {
			incurred, ok := deprecationDate(t, title, "Incurred", deprecationIncurredRe, block,
				"A migration is a deprecation incurred the day it lands (D127); recover the date with "+
					"`git log -S '<symbol>' --format='%as %h'` rather than estimating it — an estimated "+
					"date cannot answer the only question the register exists to answer.")
			due, dueOK := deprecationDate(t, title, "Due", deprecationDueRe, block,
				"Pick one from the register's recommended grace periods (none / 6mo internal / 12mo "+
					"user-facing) and say which you used — the grace is a property of the population "+
					"stranded, not of how much the code annoys you.")
			if ok && dueOK && due.Before(incurred) {
				t.Errorf("entry %q is Due %s, before it was Incurred %s — one of the two dates is wrong",
					title, due.Format("2006-01-02"), incurred.Format("2006-01-02"))
			}
			if !strings.Contains(block, "**Retire by:**") {
				t.Errorf("entry %q states no '**Retire by:**' — an entry with no stated way out is a "+
					"liability with no exit, which is the condition the register exists to end", title)
			}
		})
	}
}

// deprecationDate pulls one dated field out of a register entry and parses it,
// reporting a field-specific remedy rather than a bare regex miss — the entries
// are written by whoever adds the compatibility, so the error has to teach the
// convention at the moment it is broken.
func deprecationDate(t *testing.T, title, field string, re *regexp.Regexp, block, remedy string) (time.Time, bool) {
	t.Helper()
	m := re.FindStringSubmatch(block)
	if m == nil {
		t.Errorf("entry %q has no parseable '**%s:** YYYY-MM-DD'.\n%s", title, field, remedy)
		return time.Time{}, false
	}
	d, err := time.Parse("2006-01-02", m[1])
	if err != nil {
		t.Errorf("entry %q has an unparseable %s date %q: %v", title, field, m[1], err)
		return time.Time{}, false
	}
	return d, true
}
