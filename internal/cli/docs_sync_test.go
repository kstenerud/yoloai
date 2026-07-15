// ABOUTME: Gates that keep shipped help text (internal/cli/helpcmd/help/*.md)
// ABOUTME: and docs/GUIDE.md honest about config keys and CLI flags — nothing
// ABOUTME: else in `make check` reads prose. See D116 for the motivating bug.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/spf13/cobra"
)

// Why these gates exist: the config key `backend` was renamed to
// `container_backend` in March 2026. The embedded help topic
// (internal/cli/helpcmd/help/config.md, //go:embed'd into the shipped
// binary by helpcmd/help.go) went on advertising the dead key through all
// 15 releases from v0.2.0 to v0.8.0 — four months — with `make check`
// green the entire time, because nothing typechecks prose. A human caught
// it, not a test (D116). Separately, the project's first outside PR (#36)
// fixed one block of a help topic and missed a settings table 13 lines
// above naming the same dead key — proof that "the doc mentions the right
// thing somewhere" isn't enough; every surface has to agree.
//
// TestDocsConfigKeysResolve (Gate 1) scans the settings tables and runnable
// `yoloai config set|get|reset <key>` examples in shipped help text and
// docs/GUIDE.md, and asserts every config key named there resolves via
// config.IsKnownConfigPath. Without this gate, a config.go rename silently
// leaves stale key names in shipped UI indefinitely — exactly D116's bug.
//
// TestDocsFlagsExist (Gate 2) extracts every `--flag-name` token from
// shipped help text and asserts it exists somewhere in the live cobra
// command tree. Without this gate, a renamed or removed flag can be
// advertised forever the same way `backend` was.
//
// Both gates are deliberately narrow: they can only catch a name that is
// *absent* from the current schema/command tree, not a name that exists
// but means something different, or prose that's simply wrong in some
// other way. That's the ceiling of what's mechanically gateable here — see
// D117 §16 for the doc-sweep process that covers the rest.

// docSource is one shipped-text file scanned by Gate 1.
type docSource struct {
	relPath string // relative to repo root, used in failure messages
	content string
}

// lineMatch is a candidate token found at a specific line of a docSource,
// carried through so failure messages can point at an exact location.
type lineMatch struct {
	lineNo int
	token  string
}

// repoRoot walks up from the test's working directory (internal/cli/ when
// run via `go test ./internal/cli/...`) to the directory containing go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("walked up to filesystem root without finding go.mod (started at %s)", dir)
		}
		dir = parent
	}
}

// loadConfigKeyDocSources returns the Gate-1 shipped-text corpus: every
// embedded help topic plus docs/GUIDE.md. Both are named explicitly in
// procedures/pull-requests.md's "name sweep" surface list.
func loadConfigKeyDocSources(t *testing.T, root string) []docSource {
	t.Helper()
	helpFiles, err := filepath.Glob(filepath.Join(root, "internal", "cli", "helpcmd", "help", "*.md"))
	if err != nil {
		t.Fatalf("glob help topics: %v", err)
	}
	if len(helpFiles) == 0 {
		t.Fatal("no help topics matched internal/cli/helpcmd/help/*.md — glob pattern or repoRoot() is broken, this gate is silently checking nothing")
	}
	paths := make([]string, 0, len(helpFiles)+1)
	paths = append(paths, helpFiles...)
	paths = append(paths, filepath.Join(root, "docs", "GUIDE.md"))

	sources := make([]docSource, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // G304: path from a glob rooted at repoRoot(), not user input
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		sources = append(sources, docSource{relPath: rel, content: string(data)})
	}
	return sources
}

// isSectionHeader reports whether line is a topic-file section header: an
// unindented, all-caps line (e.g. "KEY SETTINGS", "EXAMPLES", "COMMANDS").
// Indented rows (settings-table entries, example commands) never match, so
// this can't misfire on table content.
func isSectionHeader(line string) bool {
	if line == "" || line[0] == ' ' {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed != strings.ToUpper(trimmed) {
		return false
	}
	return strings.ContainsAny(trimmed, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

// isSettingsSectionHeader reports whether a section header line starts a
// KEY SETTINGS-style table. Matched by substring ("SETTINGS") rather than
// an exact "KEY SETTINGS" literal so a future help topic's settings table
// (not just config.md's) is covered automatically.
func isSettingsSectionHeader(line string) bool {
	return isSectionHeader(line) && strings.Contains(line, "SETTINGS")
}

// settingsRowRe matches a KEY SETTINGS-style table row: exactly two leading
// spaces, a key token, then a 2+ space gutter before the description.
// Continuation lines (wrapped descriptions) are indented past column 2 to
// align under the description column, so they never match — only real key
// rows do. See TestDocsConfigKeysResolve_RejectsStaleKey for a worked
// example of what this does and doesn't match.
var settingsRowRe = regexp.MustCompile(`^  (\S+)\s{2,}\S`)

// extractKeySettingsKeys scans content for KEY SETTINGS-style table rows
// and returns the key token from each, with its 1-based line number.
func extractKeySettingsKeys(content string) []lineMatch {
	var out []lineMatch
	inBlock := false
	for i, line := range strings.Split(content, "\n") {
		if isSectionHeader(line) {
			inBlock = isSettingsSectionHeader(line)
			continue
		}
		if !inBlock {
			continue
		}
		if m := settingsRowRe.FindStringSubmatch(line); m != nil {
			out = append(out, lineMatch{lineNo: i + 1, token: m[1]})
		}
	}
	return out
}

// configCmdRe matches a runnable `yoloai config set|get|reset <key>` example
// line and captures the token immediately following the subcommand — the
// key (for set, followed separately by a value; get/reset take only a key).
var configCmdRe = regexp.MustCompile(`\byoloai config (?:set|get|reset)\s+(\S+)`)

// cleanConfigCandidate strips trailing markdown/prose punctuation from a
// regex-captured token and rejects anything that isn't a literal key: a
// placeholder like <key>/[key], a following flag like --json, or a trailing
// "# comment" marker on a bare `config get`/`config reset` example line with
// no key argument at all. Returns "" for anything that should not be checked.
func cleanConfigCandidate(tok string) string {
	tok = strings.TrimRight(tok, "`|,;)")
	if tok == "" || strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "#") || strings.ContainsAny(tok, "<>[]`") {
		return ""
	}
	return tok
}

// extractConfigCommandKeys scans content for `yoloai config set|get|reset
// <key>` example lines (in prose, fenced code blocks, or table cells —
// the regex doesn't care which) and returns each literal key found, with
// its 1-based line number. Placeholders like <key> are filtered out by
// cleanConfigCandidate, not treated as unresolved keys.
func extractConfigCommandKeys(content string) []lineMatch {
	var out []lineMatch
	for i, line := range strings.Split(content, "\n") {
		m := configCmdRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		token := cleanConfigCandidate(m[1])
		if token == "" {
			continue
		}
		out = append(out, lineMatch{lineNo: i + 1, token: token})
	}
	return out
}

// guideSettingsTableRowRe matches a row of docs/GUIDE.md's "### Settings"
// table: `| \`key\` | default | description |`. The key is the backtick
// span in the first column.
var guideSettingsTableRowRe = regexp.MustCompile("^\\|\\s*`([^`]+)`")

// extractGuideSettingsTableKeys scans GUIDE.md content for the "### Settings"
// markdown table specifically (matched by its exact header row, "| Key |
// Default | Description |") and returns each key cell. Scoping to that
// literal header keeps this from misfiring on GUIDE.md's many other
// `| Command | Description |`-headed tables, whose first column is a full
// command example, not a bare config key.
func extractGuideSettingsTableKeys(content string) []lineMatch {
	var out []lineMatch
	inTable := false
	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "| Key | Default | Description |":
			inTable = true
		case !inTable:
			// not in the table yet; nothing to do
		case strings.HasPrefix(trimmed, "|---"):
			// header/body separator row
		case !strings.HasPrefix(trimmed, "|"):
			inTable = false
		default:
			if m := guideSettingsTableRowRe.FindStringSubmatch(line); m != nil {
				out = append(out, lineMatch{lineNo: i + 1, token: m[1]})
			}
		}
	}
	return out
}

// TestDocsConfigKeysResolve is Gate 1: every config key named in shipped
// help text or docs/GUIDE.md must resolve via config.IsKnownConfigPath. See
// the package-level doc comment above for the motivating D116 bug.
func TestDocsConfigKeysResolve(t *testing.T) {
	root := repoRoot(t)
	sources := loadConfigKeyDocSources(t, root)

	var failures []string
	for _, src := range sources {
		var candidates []lineMatch
		candidates = append(candidates, extractKeySettingsKeys(src.content)...)
		candidates = append(candidates, extractConfigCommandKeys(src.content)...)
		if strings.HasSuffix(src.relPath, "GUIDE.md") {
			candidates = append(candidates, extractGuideSettingsTableKeys(src.content)...)
		}
		for _, c := range candidates {
			if config.IsKnownConfigPath(c.token) {
				continue
			}
			failures = append(failures, fmt.Sprintf(
				"%s:%d: config key %q does not resolve via config.IsKnownConfigPath — "+
					"it was likely renamed or removed. Fix the doc to name the current key, "+
					"or if the key is genuinely new, add it to internal/config's known-settings tables.",
				src.relPath, c.lineNo, c.token))
		}
	}

	if len(failures) > 0 {
		sort.Strings(failures)
		t.Errorf("stale config key(s) in shipped text:\n%s", strings.Join(failures, "\n"))
	}
}

// TestDocsConfigKeysResolve_RejectsStaleKey proves Gate 1 actually fires.
// A gate that has never been observed to fail is not a gate — it's an
// assumption. This feeds the production extractor (extractKeySettingsKeys,
// the same function TestDocsConfigKeysResolve calls) a synthetic KEY
// SETTINGS block containing the literal dead key from the D116 story
// (`backend`, renamed to `container_backend` in March 2026) and asserts
// two things: the extractor finds it (so the pattern-match itself is
// sound), and config.IsKnownConfigPath rejects it (so the check would have
// failed the build throughout those 15 releases, had this test existed).
func TestDocsConfigKeysResolve_RejectsStaleKey(t *testing.T) {
	synthetic := "KEY SETTINGS\n\n" +
		"  backend          Runtime backend: docker, podman\n\n" +
		"EXAMPLES\n\n" +
		"     yoloai config set backend docker\n"

	tableMatches := extractKeySettingsKeys(synthetic)
	if len(tableMatches) != 1 || tableMatches[0].token != "backend" {
		t.Fatalf("extractKeySettingsKeys did not find the expected stale 'backend' row in the synthetic "+
			"KEY SETTINGS block; got %#v — the extractor pattern itself is broken, not just the gate", tableMatches)
	}
	if config.IsKnownConfigPath(tableMatches[0].token) {
		t.Fatalf("config.IsKnownConfigPath(%q) unexpectedly returned true — this key was renamed to "+
			"container_backend in March 2026 (D116); if it now resolves, a dead alias was silently reintroduced",
			tableMatches[0].token)
	}

	cmdMatches := extractConfigCommandKeys(synthetic)
	if len(cmdMatches) != 1 || cmdMatches[0].token != "backend" {
		t.Fatalf("extractConfigCommandKeys did not find the expected stale 'backend' key in the synthetic "+
			"'yoloai config set backend docker' example line; got %#v", cmdMatches)
	}
	if config.IsKnownConfigPath(cmdMatches[0].token) {
		t.Fatalf("config.IsKnownConfigPath(%q) unexpectedly returned true for the command-example extractor",
			cmdMatches[0].token)
	}
}

// flagTokenRe extracts `--flag-name` tokens from help text. Requires a
// letter immediately after `--`, so it does not match the bare `--`
// passthrough separator (e.g. "yoloai diff <name> -- <paths>").
var flagTokenRe = regexp.MustCompile(`--[A-Za-z][A-Za-z0-9-]*`)

// docsFlagAllowlist lists `--flag-name` tokens that appear in shipped help
// text but are intentionally NOT yoloai flags — each entry names the
// foreign tool or placeholder responsible and where it appears. Every
// entry here must be independently justified; a bare allowlist is how
// gates rot (see the task instructions this test was built against).
// TestDocsFlagsExist fails loudly if an entry stops being matched, so this
// list can't silently accumulate dead slack either.
var docsFlagAllowlist = map[string]string{
	// extensions.md's usage line reads "yoloai x <name> [args...] [--flags...]" —
	// bracket-notation meaning "extension-defined flags go here", not a
	// literal flag named --flags.
	"--flags": "placeholder notation in extensions.md's usage synopsis, not a literal flag",

	// extensions.md: "Flag names with hyphens become underscores (e.g.,
	// --my-flag -> $my_flag)" — an illustrative example name for the
	// flag-to-env-var rule, not a real yoloai flag.
	"--my-flag": "illustrative example flag name in extensions.md's naming-rule explanation",

	// extensions.md's from-issue example script shells out to the gh CLI:
	// `gh issue view "$issue" --repo "$repo" --json title -q .title`.
	// --repo is gh's flag, not yoloai's.
	"--repo": "gh CLI flag used in extensions.md's example extension script, not a yoloai flag",

	// cleanup.md's Kata-stuck-task recovery recipe: `sudo ctr -n yoloai
	// tasks delete --force yoloai-<sandbox-name>`. --force is containerd's
	// ctr CLI flag; yoloai has no --force flag anywhere.
	"--force": "containerd `ctr` CLI flag in cleanup.md's stuck-task recovery recipe, not a yoloai flag",

	// flags.md/security.md describe container-privileged isolation mode as
	// running "Default runc with --privileged" / "(--privileged, for
	// Docker-in-Docker workloads)" — that's docker run's own flag, used
	// internally by that isolation mode. The yoloai-facing knob is
	// `--isolation container-privileged`, which IS a real flag+value and
	// needs no allowlisting.
	"--privileged": "underlying `docker run --privileged` flag described by the container-privileged isolation mode, not a yoloai flag",
}

// flagExistsInTree reports whether a long flag named name (without the
// leading "--") is registered anywhere in cmd's subtree: as a local flag on
// some command, or as a persistent flag on that command or an ancestor.
// cmd.InheritedFlags() has the side effect of merging persistent flags
// (own + all ancestors') into cmd.Flags(), which is what makes the
// subsequent Lookup complete — see cobra's Command.mergePersistentFlags.
func flagExistsInTree(cmd *cobra.Command, name string) bool {
	cmd.InheritedFlags() // triggers the persistent-flag merge into cmd.Flags()
	if cmd.Flags().Lookup(name) != nil {
		return true
	}
	for _, sub := range cmd.Commands() {
		if flagExistsInTree(sub, name) {
			return true
		}
	}
	return false
}

// scanFileForStaleFlags reads one help topic file and returns a failure
// message for every `--flag-name` token that is neither allowlisted nor
// present in rootCmd's flag tree. Allowlist hits are marked in used so the
// caller can also detect entries that never matched anything.
func scanFileForStaleFlags(t *testing.T, rootCmd *cobra.Command, root, path string, used map[string]bool) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from a glob rooted at repoRoot(), not user input
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}

	var failures []string
	for i, line := range strings.Split(string(data), "\n") {
		for _, tok := range flagTokenRe.FindAllString(line, -1) {
			if _, ok := docsFlagAllowlist[tok]; ok {
				used[tok] = true
				continue
			}
			if flagExistsInTree(rootCmd, strings.TrimPrefix(tok, "--")) {
				continue
			}
			failures = append(failures, fmt.Sprintf(
				"%s:%d: flag %q is advertised in help text but does not exist on any cobra command. "+
					"Fix: rename/re-add the flag to match, or if it names a foreign tool's flag or a "+
					"placeholder (not yoloai's), add it to docsFlagAllowlist in this file with a justification.",
				rel, i+1, tok))
		}
	}
	return failures
}

// staleAllowlistEntries returns a failure message for every docsFlagAllowlist
// entry not present in used — an allowlist entry that never matched
// anything is dead weight that would hide a future real regression.
func staleAllowlistEntries(used map[string]bool) []string {
	var failures []string
	for tok, why := range docsFlagAllowlist {
		if !used[tok] {
			failures = append(failures, fmt.Sprintf(
				"docsFlagAllowlist entry %q (%s) was never matched in internal/cli/helpcmd/help/*.md — "+
					"remove the stale entry, a bare allowlist is how gates rot", tok, why))
		}
	}
	return failures
}

// TestDocsFlagsExist is Gate 2: every --flag-name token advertised in
// shipped help text must exist somewhere in the live cobra command tree
// (as a local or persistent/global flag), unless explicitly allowlisted as
// belonging to a foreign tool or a documentation placeholder. See the
// package-level doc comment above for why this gate exists.
func TestDocsFlagsExist(t *testing.T) {
	root := repoRoot(t)
	helpFiles, err := filepath.Glob(filepath.Join(root, "internal", "cli", "helpcmd", "help", "*.md"))
	if err != nil {
		t.Fatalf("glob help topics: %v", err)
	}
	if len(helpFiles) == 0 {
		t.Fatal("no help topics matched internal/cli/helpcmd/help/*.md — this gate is silently checking nothing")
	}

	testutil.IsolatedHome(t)
	rootCmd := NewRootCmd("test", "test", "test")

	used := make(map[string]bool, len(docsFlagAllowlist))
	var failures []string
	for _, path := range helpFiles {
		failures = append(failures, scanFileForStaleFlags(t, rootCmd, root, path, used)...)
	}
	failures = append(failures, staleAllowlistEntries(used)...)

	if len(failures) > 0 {
		sort.Strings(failures)
		t.Errorf("stale/nonexistent flags referenced in shipped help text:\n%s", strings.Join(failures, "\n"))
	}
}

// TestDocsFlagsExist_DetectsMissingFlag proves Gate 2 actually fires,
// independent of current doc content: flagExistsInTree must find a flag
// known to be real (--json, a persistent flag registered in NewRootCmd)
// and must NOT find one that is not registered anywhere.
func TestDocsFlagsExist_DetectsMissingFlag(t *testing.T) {
	testutil.IsolatedHome(t)
	rootCmd := NewRootCmd("test", "test", "test")

	if !flagExistsInTree(rootCmd, "json") {
		t.Fatal("sanity check failed: --json is a real persistent flag (registered in NewRootCmd) but " +
			"flagExistsInTree did not find it — the checker itself is broken")
	}
	if flagExistsInTree(rootCmd, "this-flag-does-not-exist-anywhere") {
		t.Fatal("sanity check failed: flagExistsInTree reported a nonexistent flag as present — " +
			"the gate would never fail on a genuinely stale flag")
	}
}
