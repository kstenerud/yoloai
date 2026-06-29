package workflow

import (
	"errors"
	"path/filepath"
	"testing"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseApplyArgs_Empty(t *testing.T) {
	cmd, _ := makeDiffCmd([]string{"name"})
	refs, paths := parseApplyArgs(nil, cmd, 1)
	assert.Nil(t, refs)
	assert.Nil(t, paths)
}

func TestParseApplyArgs_AllRefs(t *testing.T) {
	// "apply name abc123 def456" — both look like refs
	cmd, args := makeDiffCmd([]string{"name", "abc123", "def456"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Equal(t, []string{"abc123", "def456"}, refs)
	assert.Nil(t, paths)
}

func TestParseApplyArgs_AllPaths(t *testing.T) {
	// "apply name src/ lib/" — neither looks like a ref
	cmd, args := makeDiffCmd([]string{"name", "src/", "lib/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Nil(t, refs)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseApplyArgs_MixedTreatedAsPaths(t *testing.T) {
	// "apply name abc123 src/" — mixed: first is ref-like, second is not.
	// Since not all args look like refs, all become paths.
	cmd, args := makeDiffCmd([]string{"name", "abc123", "src/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Nil(t, refs)
	assert.Equal(t, []string{"abc123", "src/"}, paths)
}

func TestParseApplyArgs_WithDash_RefsAndPaths(t *testing.T) {
	// "apply name abc123 -- src/"
	cmd, args := makeDiffCmd([]string{"name", "abc123", "--", "src/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Equal(t, []string{"abc123"}, refs)
	assert.Equal(t, []string{"src/"}, paths)
}

func TestParseApplyArgs_WithDash_OnlyPaths(t *testing.T) {
	// "apply name -- src/ lib/"
	cmd, args := makeDiffCmd([]string{"name", "--", "src/", "lib/"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Empty(t, refs)
	assert.Equal(t, []string{"src/", "lib/"}, paths)
}

func TestParseApplyArgs_WithDash_OnlyRefs(t *testing.T) {
	// "apply name abc123 def456 --"
	cmd, args := makeDiffCmd([]string{"name", "abc123", "def456", "--"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Equal(t, []string{"abc123", "def456"}, refs)
	assert.Empty(t, paths)
}

func TestParseApplyArgs_SingleRef(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "abcdef12"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Equal(t, []string{"abcdef12"}, refs)
	assert.Nil(t, paths)
}

func TestParseApplyArgs_SinglePath(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "main.go"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Nil(t, refs)
	assert.Equal(t, []string{"main.go"}, paths)
}

func TestParseApplyArgs_RangeRef(t *testing.T) {
	cmd, args := makeDiffCmd([]string{"name", "abcd..ef12"})
	rest := args[1:]
	refs, paths := parseApplyArgs(rest, cmd, 1)
	assert.Equal(t, []string{"abcd..ef12"}, refs)
	assert.Nil(t, paths)
}

func TestApplyAll_WithPatches_ReturnsUsageError(t *testing.T) {
	home := t.TempDir()
	cliutil.SetRootLayout(cliutil.LayoutForDataDir(filepath.Join(home, ".yoloai")))
	t.Cleanup(func() { cliutil.SetRootLayout(config.Layout{}) })

	cmd := NewApplyCmd()
	cmd.SetArgs([]string{"mybox", "--all", "--patches", "/tmp/p"})
	err := cmd.Execute()
	assert.Error(t, err)
}

// --- dispatchApply guard-clause tests ---

func TestDispatchApply_RefsAndNoCommit_UsageError(t *testing.T) {
	cmd := &cobra.Command{}
	dir := yoloai.DirInfo{Mode: yoloai.DirModeCopy, HostPath: "/proj"}
	flags := applyFlags{noCommit: true}
	refs := []string{"abc123"}

	err := dispatchApply(cmd, "mybox", "/proj", dir, refs, nil, flags)

	require.Error(t, err)
	var ue *yoerrors.UsageError
	assert.True(t, errors.As(err, &ue), "expected *yoerrors.UsageError, got %T: %v", err, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestDispatchApply_RWDir_UsageError(t *testing.T) {
	cmd := &cobra.Command{}
	dir := yoloai.DirInfo{Mode: yoloai.DirModeRW, HostPath: "/proj"}
	flags := applyFlags{}

	err := dispatchApply(cmd, "mybox", "/proj", dir, nil, nil, flags)

	require.Error(t, err)
	var ue *yoerrors.UsageError
	assert.True(t, errors.As(err, &ue), "expected *yoerrors.UsageError, got %T: %v", err, err)
	assert.Contains(t, err.Error(), ":rw")
}

// --- buildTagsByCommit tests ---

func TestBuildTagsByCommit_EmptyInput(t *testing.T) {
	m := buildTagsByCommit(nil)
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestBuildTagsByCommit_SameSHADifferentCase(t *testing.T) {
	tags := []yoloai.TagInfo{
		{SHA: "ABCDEF123456", Name: "v1.0"},
		{SHA: "abcdef123456", Name: "v1.1"},
	}
	m := buildTagsByCommit(tags)
	assert.Len(t, m, 1)
	assert.ElementsMatch(t, []string{"v1.0", "v1.1"}, m["abcdef123456"])
}

func TestBuildTagsByCommit_DistinctSHAs(t *testing.T) {
	tags := []yoloai.TagInfo{
		{SHA: "aaaa1111", Name: "v1.0"},
		{SHA: "bbbb2222", Name: "v2.0"},
	}
	m := buildTagsByCommit(tags)
	assert.Len(t, m, 2)
	assert.Equal(t, []string{"v1.0"}, m["aaaa1111"])
	assert.Equal(t, []string{"v2.0"}, m["bbbb2222"])
}
