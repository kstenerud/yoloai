package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/kstenerud/yoloai/sandbox"
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
	ref, paths := parseDiffArgs(nil, cmd)
	assert.Equal(t, "", ref)
	assert.Nil(t, paths)
}

func TestParseDiffArgs_RefOnly(t *testing.T) {
	// Simulate: "diff name abc123" (no --)
	cmd, args := makeDiffCmd([]string{"name", "abc123"})
	rest := args[1:] // consume name
	ref, paths := parseDiffArgs(rest, cmd)
	assert.Equal(t, "abc123", ref)
	assert.Empty(t, paths)
}

func TestParseDiffArgs_PathOnly(t *testing.T) {
	// Simulate: "diff name src/main.go"
	cmd, args := makeDiffCmd([]string{"name", "src/main.go"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd)
	assert.Equal(t, "", ref)
	assert.Equal(t, []string{"src/main.go"}, paths)
}

func TestParseDiffArgs_MultiplePaths(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "src/", "lib/"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd)
	assert.Equal(t, "", ref)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseDiffArgs_RefAndPathsWithDash(t *testing.T) {
	// Simulate: "diff name abc123 -- src/"
	cmd, args := makeDiffCmd([]string{"name", "abc123", "--", "src/"})
	rest := args[1:] // ["abc123", "src/"]
	ref, paths := parseDiffArgs(rest, cmd)
	assert.Equal(t, "abc123", ref)
	assert.Equal(t, []string{"src/"}, paths)
}

func TestParseDiffArgs_PathsOnlyWithDash(t *testing.T) {
	// Simulate: "diff name -- src/ lib/"
	cmd, args := makeDiffCmd([]string{"name", "--", "src/", "lib/"})
	rest := args[1:] // ["src/", "lib/"]
	ref, paths := parseDiffArgs(rest, cmd)
	assert.Equal(t, "", ref)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseDiffArgs_RangeRef(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "abcd..1234"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd)
	assert.Equal(t, "abcd..1234", ref)
	assert.Empty(t, paths)
}

func TestParseDiffArgs_RefWithDashNoPaths(t *testing.T) {
	// "diff name abc123 --" (dash at end, no paths after)
	cmd, args := makeDiffCmd([]string{"name", "abc123", "--"})
	rest := args[1:]
	ref, paths := parseDiffArgs(rest, cmd)
	assert.Equal(t, "abc123", ref)
	assert.Nil(t, paths)
}

// hasOverlayDirs tests

func TestHasOverlayDirs_WorkdirOverlay(t *testing.T) {
	meta := &sandbox.Meta{
		Workdir: sandbox.WorkdirMeta{Mode: "overlay"},
	}
	assert.True(t, hasOverlayDirs(meta))
}

func TestHasOverlayDirs_AuxOverlay(t *testing.T) {
	meta := &sandbox.Meta{
		Workdir: sandbox.WorkdirMeta{Mode: "copy"},
		Directories: []sandbox.DirMeta{
			{Mode: "rw"},
			{Mode: "overlay"},
		},
	}
	assert.True(t, hasOverlayDirs(meta))
}

func TestHasOverlayDirs_NoneOverlay(t *testing.T) {
	meta := &sandbox.Meta{
		Workdir: sandbox.WorkdirMeta{Mode: "copy"},
		Directories: []sandbox.DirMeta{
			{Mode: "copy"},
			{Mode: "rw"},
		},
	}
	assert.False(t, hasOverlayDirs(meta))
}
