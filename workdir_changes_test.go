// ABOUTME: Unit tests for changesFromCopyflow — the pure mapper from
// ABOUTME: []copyflow.FileChange to *Changes that sums non-binary totals.

package yoloai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/copyflow"
)

func TestChangesFromCopyflow_Empty(t *testing.T) {
	got := changesFromCopyflow(nil)
	require.NotNil(t, got)
	assert.Empty(t, got.Files)
	assert.Equal(t, 0, got.Additions)
	assert.Equal(t, 0, got.Deletions)
}

func TestChangesFromCopyflow_EmptySlice(t *testing.T) {
	got := changesFromCopyflow([]copyflow.FileChange{})
	require.NotNil(t, got)
	assert.Empty(t, got.Files)
	assert.Equal(t, 0, got.Additions)
	assert.Equal(t, 0, got.Deletions)
}

func TestChangesFromCopyflow_AllText(t *testing.T) {
	src := []copyflow.FileChange{
		{Path: "a.go", Additions: 10, Deletions: 3},
		{Path: "b.go", Additions: 5, Deletions: 7},
	}
	got := changesFromCopyflow(src)
	require.NotNil(t, got)
	require.Len(t, got.Files, 2)
	// Per-file fields copied through.
	assert.Equal(t, "a.go", got.Files[0].Path)
	assert.Equal(t, 10, got.Files[0].Additions)
	assert.Equal(t, 3, got.Files[0].Deletions)
	assert.False(t, got.Files[0].Binary)
	assert.Equal(t, "b.go", got.Files[1].Path)
	assert.Equal(t, 5, got.Files[1].Additions)
	assert.Equal(t, 7, got.Files[1].Deletions)
	assert.False(t, got.Files[1].Binary)
	// Totals are the correct sums.
	assert.Equal(t, 15, got.Additions)
	assert.Equal(t, 10, got.Deletions)
}

func TestChangesFromCopyflow_MixedTextAndBinary(t *testing.T) {
	// git --numstat reports -1/-1 for binary files.
	src := []copyflow.FileChange{
		{Path: "main.go", Additions: 4, Deletions: 1},
		{Path: "image.png", Additions: -1, Deletions: -1, Binary: true},
		{Path: "util.go", Additions: 2, Deletions: 0},
	}
	got := changesFromCopyflow(src)
	require.NotNil(t, got)
	require.Len(t, got.Files, 3, "binary file must appear in Files")

	// Binary file is present with Binary flag set.
	assert.Equal(t, "image.png", got.Files[1].Path)
	assert.Equal(t, -1, got.Files[1].Additions)
	assert.Equal(t, -1, got.Files[1].Deletions)
	assert.True(t, got.Files[1].Binary)

	// Totals exclude the binary file's -1 values.
	assert.Equal(t, 6, got.Additions, "binary Additions must not be counted")
	assert.Equal(t, 1, got.Deletions, "binary Deletions must not be counted")
}
