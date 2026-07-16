// ABOUTME: ProjectFileSet must answer exactly what CopyProjectDir writes, since
// ABOUTME: reset copies by the first and create by the second — if they drift, a
// ABOUTME: reset stops reproducing the copy and DF117 comes back by another door.
package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trackedAndUntracked stands in for git.ListProjectFiles, which this package
// takes as an injected function so it stays git-free.
func trackedAndUntracked(t *testing.T, dir string) []string {
	t.Helper()
	out := runGit(t, dir, "ls-files", "--cached", "--others", "--exclude-standard")
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// actualCopyContents walks what CopyProjectDir wrote and returns dst-relative
// paths, minus .git, which ProjectFileSet excludes by contract.
func actualCopyContents(t *testing.T, dst string) []string {
	t.Helper()
	var got []string
	require.NoError(t, filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		rel, relErr := filepath.Rel(dst, path)
		require.NoError(t, relErr)
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		got = append(got, rel)
		return nil
	}))
	sort.Strings(got)
	return got
}

// projectFixture builds a source tree exercising every exclusion CopyProjectDir
// applies, plus the filename shapes that have defeated pattern-based filters.
func projectFixture(t *testing.T) string {
	src := t.TempDir()
	makeRepo(t, src) // a.txt, .gitignore (ignores ignored.log), ignored.log, one commit
	write(t, filepath.Join(src, "sub", "b.txt"), "b")
	write(t, filepath.Join(src, "node_modules", "dep.js"), "artifact")
	write(t, filepath.Join(src, "sub", "__pycache__", "x.pyc"), "artifact")
	write(t, filepath.Join(src, "yoloai-bugreport-123.md"), "bugreport")
	write(t, filepath.Join(src, "weird[1].txt"), "bracket name")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "emptydir"), 0o750))
	runGit(t, src, "add", "-A")
	runGit(t, src, "commit", "-qm", "fixture")
	return src
}

// The gate: whatever CopyProjectDir writes, ProjectFileSet must have named.
func TestProjectFileSet_MatchesCopyProjectDir(t *testing.T) {
	for _, tc := range []struct {
		name           string
		includeIgnored bool
	}{
		{"copy honors gitignore", false},
		{"copy-all takes everything", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := projectFixture(t)
			listFn := func() ([]string, bool, error) {
				return trackedAndUntracked(t, src), true, nil
			}

			dst := filepath.Join(t.TempDir(), "out")
			require.NoError(t, CopyProjectDir(src, dst, tc.includeIgnored, true, listFn))

			want, err := ProjectFileSet(src, tc.includeIgnored, listFn)
			require.NoError(t, err)
			sort.Strings(want)

			// Nothing CopyProjectDir wrote may be unaccounted for, or the prune
			// would delete a file the copy just placed. Parent directories count
			// as accounted for: the copy creates them implicitly rather than
			// listing them, which is why the prune works through the closure.
			keep := pathsWithAncestors(want)
			for _, got := range actualCopyContents(t, dst) {
				assert.True(t, keep[got],
					"CopyProjectDir wrote %q but ProjectFileSet does not account for it: "+
						"reset would copy it and then prune it away", got)
			}

			// And nothing named may be missing, or reset would leave the sandbox
			// short of a file create would have given it.
			for _, f := range want {
				assert.True(t, exists(filepath.Join(dst, f)),
					"ProjectFileSet names %q but CopyProjectDir did not write it", f)
			}
		})
	}
}

func TestProjectFileSet_CopyExcludesIgnoredAndArtifacts(t *testing.T) {
	src := projectFixture(t)
	got, err := ProjectFileSet(src, false, func() ([]string, bool, error) {
		return trackedAndUntracked(t, src), true, nil
	})
	require.NoError(t, err)

	assert.Contains(t, got, "a.txt")
	assert.Contains(t, got, filepath.Join("sub", "b.txt"))
	assert.NotContains(t, got, "ignored.log", "the whole point of :copy: a gitignored file never enters the sandbox")
	assert.NotContains(t, got, filepath.Join("node_modules", "dep.js"))
	assert.NotContains(t, got, "yoloai-bugreport-123.md")
	assert.NotContains(t, got, ".git")
}

func TestProjectFileSet_CopyAllIncludesIgnoredButNotArtifacts(t *testing.T) {
	src := projectFixture(t)
	got, err := ProjectFileSet(src, true, func() ([]string, bool, error) {
		t.Fatal("listProjectFiles must not be consulted for :copy-all")
		return nil, false, nil
	})
	require.NoError(t, err)

	assert.Contains(t, got, "ignored.log", ":copy-all's purpose is including ignored files")
	assert.Contains(t, got, "emptydir", "CopyDir preserves empty dirs, so the set must name them")
	assert.NotContains(t, got, filepath.Join("node_modules", "dep.js"), ":copy-all still drops build artifacts")
	assert.NotContains(t, got, filepath.Join("sub", "__pycache__", "x.pyc"))
	assert.NotContains(t, got, "yoloai-bugreport-123.md")
	assert.NotContains(t, got, ".git")
}

// A filename containing shell/rsync wildcard characters is just a name: the set
// holds paths, and the prune compares paths. Reset once mirrored the tree with
// rsync, where a denylist would have read "weird[1].txt" as a character class
// and let the file through — dropping rsync is what retired that whole class of
// bug, and this test is the reminder not to reintroduce it (DF117).
func TestProjectFileSet_NamesWildcardFilenamesLiterally(t *testing.T) {
	src := projectFixture(t)
	got, err := ProjectFileSet(src, true, func() ([]string, bool, error) { return nil, false, nil })
	require.NoError(t, err)
	assert.Contains(t, got, "weird[1].txt")
}

func TestProjectFileSet_NonRepoFallsBackToFullWalk(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "node_modules", "dep.js"), "artifact")

	got, err := ProjectFileSet(src, false, func() ([]string, bool, error) {
		return nil, false, nil // not a repo: no gitignore semantics to honor
	})
	require.NoError(t, err)
	assert.Contains(t, got, "a.txt")
	assert.NotContains(t, got, filepath.Join("node_modules", "dep.js"))
}

func TestProjectFileSet_SkipsTrackedButDeleted(t *testing.T) {
	src := t.TempDir()
	makeRepo(t, src)
	got, err := ProjectFileSet(src, false, func() ([]string, bool, error) {
		return []string{"a.txt", "gone.txt"}, true, nil
	})
	require.NoError(t, err)
	assert.Contains(t, got, "a.txt")
	assert.NotContains(t, got, "gone.txt", "tracked but deleted from the work tree: nothing to copy")
}

func TestWantsGitDir(t *testing.T) {
	assert.True(t, WantsGitDir(false, true), ":copy preserving history keeps .git")
	assert.True(t, WantsGitDir(true, false), ":copy-all takes the source wholesale")
	assert.False(t, WantsGitDir(false, false), "copy-strict strips history")
}
