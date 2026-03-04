package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseApplyArgs_Empty(t *testing.T) {
	cmd, _ := makeDiffCmd([]string{"name"})
	refs, paths := parseApplyArgs(nil, cmd)
	assert.Nil(t, refs)
	assert.Nil(t, paths)
}

func TestParseApplyArgs_AllRefs(t *testing.T) {
	// "apply name abc123 def456" — both look like refs
	cmd, args := makeDiffCmd([]string{"name", "abc123", "def456"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Equal(t, []string{"abc123", "def456"}, refs)
	assert.Nil(t, paths)
}

func TestParseApplyArgs_AllPaths(t *testing.T) {
	// "apply name src/ lib/" — neither looks like a ref
	cmd, args := makeDiffCmd([]string{"name", "src/", "lib/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Nil(t, refs)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseApplyArgs_MixedTreatedAsPaths(t *testing.T) {
	// "apply name abc123 src/" — mixed: first is ref-like, second is not.
	// Since not all args look like refs, all become paths.
	cmd, args := makeDiffCmd([]string{"name", "abc123", "src/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Nil(t, refs)
	assert.Equal(t, []string{"abc123", "src/"}, paths)
}

func TestParseApplyArgs_WithDash_RefsAndPaths(t *testing.T) {
	// "apply name abc123 -- src/"
	cmd, args := makeDiffCmd([]string{"name", "abc123", "--", "src/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Equal(t, []string{"abc123"}, refs)
	assert.Equal(t, []string{"src/"}, paths)
}

func TestParseApplyArgs_WithDash_OnlyPaths(t *testing.T) {
	// "apply name -- src/ lib/"
	cmd, args := makeDiffCmd([]string{"name", "--", "src/", "lib/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Empty(t, refs)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseApplyArgs_WithDash_OnlyRefs(t *testing.T) {
	// "apply name abc123 def456 --"
	cmd, args := makeDiffCmd([]string{"name", "abc123", "def456", "--"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Equal(t, []string{"abc123", "def456"}, refs)
	assert.Empty(t, paths)
}

func TestParseApplyArgs_SingleRef(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "abcdef12"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Equal(t, []string{"abcdef12"}, refs)
	assert.Nil(t, paths)
}

func TestParseApplyArgs_SinglePath(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "main.go"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Nil(t, refs)
	assert.Equal(t, []string{"main.go"}, paths)
}

func TestParseApplyArgs_RangeRef(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "abcd..ef12"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd)
	assert.Equal(t, []string{"abcd..ef12"}, refs)
	assert.Nil(t, paths)
}
