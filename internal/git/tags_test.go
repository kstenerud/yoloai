// ABOUTME: CreateTag (including names starting with '-' not being parsed as a
// ABOUTME: flag) and BuildSHAMapByMatching's commit-to-tag SHA lookup.

package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateTag_Lightweight creates a lightweight tag and verifies it points at
// the requested commit.
func TestCreateTag_Lightweight(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "f", "v1")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")

	g := NewTestHostWithEnv(testEnv())
	sha, err := g.HeadSHA(ctx, dir)
	require.NoError(t, err)

	require.NoError(t, g.CreateTag(ctx, dir, "v1.0", sha, ""))

	out, err := g.Run(ctx, dir, "rev-list", "-n", "1", "v1.0")
	require.NoError(t, err)
	assert.Equal(t, sha, firstLine(out))
}

// TestCreateTag_DashLeadingNameNotParsedAsFlag ensures an agent-planted
// dash-leading tag refname is passed as a positional name (after "--"), not
// parsed by git as an option. Without the "--" terminator git reports
// "unknown option"; with it, git treats the value as a (rejected) tag name.
func TestCreateTag_DashLeadingNameNotParsedAsFlag(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "f", "v1")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")

	g := NewTestHostWithEnv(testEnv())
	sha, err := g.HeadSHA(ctx, dir)
	require.NoError(t, err)

	// Lightweight and annotated forms both go through the "--" terminator.
	for _, msg := range []string{"", "annotated"} {
		err = g.CreateTag(ctx, dir, "--upload-pack=x", sha, msg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid tag name")
		assert.NotContains(t, err.Error(), "unknown option")
	}
}

// TestCreateTag_DuplicateFails reports a clear error when the tag already exists.
func TestCreateTag_DuplicateFails(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "f", "v1")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")

	g := NewTestHostWithEnv(testEnv())
	sha, err := g.HeadSHA(ctx, dir)
	require.NoError(t, err)

	require.NoError(t, g.CreateTag(ctx, dir, "dup", sha, ""))
	err = g.CreateTag(ctx, dir, "dup", sha, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// TestBuildSHAMapByMatching pairs a sandbox commit to the host commit that
// shares its author, timestamp, and subject. Both repos hold the same commit
// (cloned), so the matcher should map the sandbox SHA to the host SHA.
func TestBuildSHAMapByMatching(t *testing.T) {
	host := t.TempDir()
	initGitRepo(t, host)
	writeTestFile(t, host, "f", "v1")
	gitAdd(t, host, ".")
	gitCommit(t, host, "shared subject")

	g := NewTestHostWithEnv(testEnv())
	hostSHA, err := g.HeadSHA(ctx, host)
	require.NoError(t, err)

	// Clone so the sandbox copy carries an identical commit (same author/date/subject)
	// with the same SHA, exercising the author/timestamp/subject match path.
	sandbox := t.TempDir()
	runGit(t, sandbox, "clone", host, ".")

	sandboxSHA, err := g.HeadSHA(ctx, sandbox)
	require.NoError(t, err)

	shaMap, err := g.BuildSHAMapByMatching(ctx, g, host, sandbox, []string{sandboxSHA})
	require.NoError(t, err)
	assert.Equal(t, hostSHA, shaMap[sandboxSHA])
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
