package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPromptPreview_MissingFile(t *testing.T) {
	dir := t.TempDir()
	result := loadPromptPreview(dir)
	assert.Equal(t, "", result)
}

func TestLoadPromptPreview_ShortContent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte("Fix the login bug"), 0600))
	result := loadPromptPreview(dir)
	assert.Equal(t, "Fix the login bug", result)
}

func TestLoadPromptPreview_NewlinesReplacedWithSpaces(t *testing.T) {
	dir := t.TempDir()
	content := "Line one\nLine two\nLine three"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte(content), 0600))
	result := loadPromptPreview(dir)
	assert.Equal(t, "Line one Line two Line three", result)
}

func TestLoadPromptPreview_CRLFReplaced(t *testing.T) {
	dir := t.TempDir()
	content := "Line one\r\nLine two\r\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte(content), 0600))
	result := loadPromptPreview(dir)
	assert.Equal(t, "Line one  Line two  ", result)
}

func TestLoadPromptPreview_TruncatesAt200Runes(t *testing.T) {
	dir := t.TempDir()
	// Create a string of exactly 250 ASCII characters
	content := strings.Repeat("a", 250)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte(content), 0600))
	result := loadPromptPreview(dir)
	assert.Equal(t, strings.Repeat("a", 200)+"...", result)
}

func TestLoadPromptPreview_ExactlyAt200(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", 200)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte(content), 0600))
	result := loadPromptPreview(dir)
	assert.Equal(t, content, result)
	assert.NotContains(t, result, "...")
}

func TestLoadPromptPreview_UnicodeRuneTruncation(t *testing.T) {
	dir := t.TempDir()
	// 201 runes of a multibyte character (each is 3 bytes in UTF-8)
	content := strings.Repeat("é", 201)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte(content), 0600))
	result := loadPromptPreview(dir)
	expected := strings.Repeat("é", 200) + "..."
	assert.Equal(t, expected, result)
	// Verify rune count, not byte count
	assert.Equal(t, 203, len([]rune(result))) // 200 + 3 for "..."
}

func TestLoadPromptPreview_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompt.txt"), []byte(""), 0600))
	result := loadPromptPreview(dir)
	assert.Equal(t, "", result)
}
