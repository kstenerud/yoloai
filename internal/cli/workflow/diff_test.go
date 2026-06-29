package workflow

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeDiffCmd creates a cobra command that properly sets ArgsLenAtDash
// by parsing the given args (including "--" if present). Returns the
// command and the args slice as cobra delivers them to RunE.
func makeDiffCmd(rawArgs []string) (*cobra.Command, []string) {
	var captured []string
	cmd := &cobra.Command{
		Use:  "test",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			captured = args
			return nil
		},
	}
	cmd.SetArgs(rawArgs)
	_ = cmd.Execute() //nolint:errcheck
	return cmd, captured
}

func TestLooksLikeRef_HexSHA4(t *testing.T) {
	assert.True(t, looksLikeRef("abc1"))
}

func TestLooksLikeRef_HexSHA12(t *testing.T) {
	assert.True(t, looksLikeRef("abcdef123456"))
}

func TestLooksLikeRef_HexSHA40(t *testing.T) {
	assert.True(t, looksLikeRef("abcdef1234567890abcdef1234567890abcdef12"))
}

func TestLooksLikeRef_HexRange(t *testing.T) {
	assert.True(t, looksLikeRef("abcd..1234"))
}

func TestLooksLikeRef_MixedCase(t *testing.T) {
	assert.True(t, looksLikeRef("AbCd1234"))
}

func TestLooksLikeRef_TooShort(t *testing.T) {
	assert.False(t, looksLikeRef("abc"))
}

func TestLooksLikeRef_TooLong(t *testing.T) {
	assert.False(t, looksLikeRef("abcdef1234567890abcdef1234567890abcdef123"))
}

func TestLooksLikeRef_NonHex(t *testing.T) {
	assert.False(t, looksLikeRef("main"))
}

func TestLooksLikeRef_Path(t *testing.T) {
	assert.False(t, looksLikeRef("src/main.go"))
}

func TestLooksLikeRef_HEAD(t *testing.T) {
	assert.False(t, looksLikeRef("HEAD"))
}

func TestLooksLikeRef_Empty(t *testing.T) {
	assert.False(t, looksLikeRef(""))
}

func TestLooksLikeRef_RangeShortSides(t *testing.T) {
	assert.True(t, looksLikeRef("abcd..efab"))
}

func TestLooksLikeRef_RangeOneSideShort(t *testing.T) {
	assert.False(t, looksLikeRef("abc..efab"))
}

func TestParseDiffArgs_Empty(t *testing.T) {
	cmd := &cobra.Command{}
	ref, paths := parseDiffArgs(nil, cmd, 1)
	assert.Equal(t, "", ref)
	assert.Nil(t, paths)
}

func TestParseDiffArgs_RefOnly(t *testing.T) {
	// Simulate: "diff name abc123" (no --)
	cmd, args := makeDiffCmd([]string{"name", "abc123"})
	rest := args[1:] // consume name
	ref, paths := parseDiffArgs(rest, cmd, 1)
	assert.Equal(t, "abc123", ref)
	assert.Empty(t, paths)
}

func TestParseDiffArgs_PathOnly(t *testing.T) {
	// Simulate: "diff name src/main.go"
	cmd, args := makeDiffCmd([]string{"name", "src/main.go"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd, 1)
	assert.Equal(t, "", ref)
	assert.Equal(t, []string{"src/main.go"}, paths)
}

func TestParseDiffArgs_MultiplePaths(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "src/", "lib/"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd, 1)
	assert.Equal(t, "", ref)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseDiffArgs_RefAndPathsWithDash(t *testing.T) {
	// Simulate: "diff name abc123 -- src/"
	cmd, args := makeDiffCmd([]string{"name", "abc123", "--", "src/"})
	rest := args[1:] // ["abc123", "src/"]
	ref, paths := parseDiffArgs(rest, cmd, 1)
	assert.Equal(t, "abc123", ref)
	assert.Equal(t, []string{"src/"}, paths)
}

func TestParseDiffArgs_PathsOnlyWithDash(t *testing.T) {
	// Simulate: "diff name -- src/ lib/"
	cmd, args := makeDiffCmd([]string{"name", "--", "src/", "lib/"})
	rest := args[1:] // ["src/", "lib/"]
	ref, paths := parseDiffArgs(rest, cmd, 1)
	assert.Equal(t, "", ref)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseDiffArgs_RangeRef(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "abcd..1234"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd, 1)
	assert.Equal(t, "abcd..1234", ref)
	assert.Empty(t, paths)
}

func TestParseDiffArgs_RefWithDashNoPaths(t *testing.T) {
	// "diff name abc123 --" (dash at end, no paths after)
	cmd, args := makeDiffCmd([]string{"name", "abc123", "--"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd, 1)
	assert.Equal(t, "abc123", ref)
	assert.Nil(t, paths)
}

func TestDiffAll_WithLogFlag_ReturnsUsageError(t *testing.T) {
	cmd := NewDiffCmd()
	err := diffAll(cmd, "mybox", nil, true, false, false)
	assert.Error(t, err)
}

func TestDiffAll_WithPositionalArgs_ReturnsUsageError(t *testing.T) {
	cmd := NewDiffCmd()
	err := diffAll(cmd, "mybox", []string{"abc123"}, false, false, false)
	assert.Error(t, err)
}

// --- writeDiffOutput tests ---

func newCmdWithBuf(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	return cmd, &buf
}

func TestWriteDiffOutput_HumanNoChanges(t *testing.T) {
	cmd, buf := newCmdWithBuf(t)
	require.NoError(t, writeDiffOutput(cmd, ""))
	assert.Contains(t, buf.String(), "No changes")
}

func TestWriteDiffOutput_HumanWithDiff(t *testing.T) {
	cmd, buf := newCmdWithBuf(t)
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new"
	require.NoError(t, writeDiffOutput(cmd, diff))
	assert.Contains(t, buf.String(), diff)
}

func TestWriteDiffOutput_JSONEmptyDiff(t *testing.T) {
	cmd, buf := newCmdWithBuf(t)
	cmd.PersistentFlags().Bool("json", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))

	require.NoError(t, writeDiffOutput(cmd, ""))

	var result map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result), "output must be valid JSON: %q", buf.String())
	assert.Equal(t, "", result["diff"])
}

func TestWriteDiffOutput_JSONNonEmptyDiff(t *testing.T) {
	cmd, buf := newCmdWithBuf(t)
	cmd.PersistentFlags().Bool("json", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))

	diff := "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-x\n+y"
	require.NoError(t, writeDiffOutput(cmd, diff))

	var result map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result), "output must be valid JSON: %q", buf.String())
	assert.Equal(t, diff, result["diff"])
}

// --- formatCommitLine tests ---

func TestFormatCommitLine_TruncatesSHATo12(t *testing.T) {
	line := formatCommitLine(1, "abcdef1234567890", "fix: something", nil)
	assert.Contains(t, line, "abcdef123456")
	assert.NotContains(t, line, "abcdef1234567890")
}

func TestFormatCommitLine_NoTags(t *testing.T) {
	line := formatCommitLine(1, "abcdef123456", "fix: something", map[string][]string{})
	assert.NotContains(t, line, "[tag:")
}

func TestFormatCommitLine_OneTag(t *testing.T) {
	tags := map[string][]string{"abcdef123456": {"v1.0"}}
	line := formatCommitLine(1, "abcdef123456", "fix: something", tags)
	assert.Contains(t, line, "[tag: v1.0]")
}

func TestFormatCommitLine_TwoTagsJoined(t *testing.T) {
	tags := map[string][]string{"abcdef123456": {"v1.0", "feature-x"}}
	line := formatCommitLine(1, "abcdef123456", "fix: something", tags)
	assert.Contains(t, line, "[tag: v1.0, feature-x]")
}

// TestFormatCommitLine_UppercaseSHAMatchesLowercaseKey verifies that
// formatCommitLine lowercases the sha argument before looking up tags, so
// a caller passing an uppercase SHA still gets the tag annotation.
func TestFormatCommitLine_UppercaseSHAMatchesLowercaseKey(t *testing.T) {
	tags := map[string][]string{"abcdef123456": {"v1.0"}}
	line := formatCommitLine(1, "ABCDEF123456", "fix: something", tags)
	assert.Contains(t, line, "[tag: v1.0]")
}
