package sandboxcmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatPromptPreview_ShortContent(t *testing.T) {
	assert.Equal(t, "Fix the login bug", formatPromptPreview("Fix the login bug"))
}

func TestFormatPromptPreview_NewlinesReplacedWithSpaces(t *testing.T) {
	assert.Equal(t, "Line one Line two Line three", formatPromptPreview("Line one\nLine two\nLine three"))
}

func TestFormatPromptPreview_CRLFReplaced(t *testing.T) {
	assert.Equal(t, "Line one  Line two  ", formatPromptPreview("Line one\r\nLine two\r\n"))
}

func TestFormatPromptPreview_TruncatesAt200Runes(t *testing.T) {
	result := formatPromptPreview(strings.Repeat("a", 250))
	assert.Equal(t, strings.Repeat("a", 200)+"...", result)
}

func TestFormatPromptPreview_ExactlyAt200(t *testing.T) {
	content := strings.Repeat("x", 200)
	result := formatPromptPreview(content)
	assert.Equal(t, content, result)
	assert.NotContains(t, result, "...")
}

func TestFormatPromptPreview_UnicodeRuneTruncation(t *testing.T) {
	// 201 runes of a multibyte character (each is 3 bytes in UTF-8).
	result := formatPromptPreview(strings.Repeat("é", 201))
	assert.Equal(t, strings.Repeat("é", 200)+"...", result)
	// Verify rune count, not byte count.
	assert.Equal(t, 203, len([]rune(result))) // 200 + 3 for "..."
}

func TestFormatPromptPreview_Empty(t *testing.T) {
	assert.Equal(t, "", formatPromptPreview(""))
}
