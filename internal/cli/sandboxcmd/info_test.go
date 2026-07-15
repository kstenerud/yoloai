package sandboxcmd

import (
	"bytes"
	"strings"
	"testing"

	yoloai "github.com/kstenerud/yoloai"
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

func netHealthTestInfo(health, detail string) *yoloai.SandboxInfo {
	return &yoloai.SandboxInfo{
		Environment:     &yoloai.Environment{Name: "embrace"},
		NetHealth:       health,
		NetHealthDetail: detail,
	}
}

func TestPrintSandboxNetwork_NetHealthWedged(t *testing.T) {
	var buf bytes.Buffer
	printSandboxNetwork(&buf, netHealthTestInfo("wedged", "guest en0 is link-local 169.254.93.37"))
	out := buf.String()
	assert.Contains(t, out, "Net health:  WEDGED (guest en0 is link-local 169.254.93.37)")
	assert.Contains(t, out, "yoloai stop embrace && yoloai start embrace")
	assert.Contains(t, out, "run 'yoloai doctor' for details")
}

func TestPrintSandboxNetwork_NetHealthOK(t *testing.T) {
	var buf bytes.Buffer
	printSandboxNetwork(&buf, netHealthTestInfo("ok", "192.168.64.12"))
	assert.Contains(t, buf.String(), "Net health:  ok (192.168.64.12)\n")
}

func TestPrintSandboxNetwork_NetHealthUnknown(t *testing.T) {
	var buf bytes.Buffer
	printSandboxNetwork(&buf, netHealthTestInfo("unknown", "guest network probe returned no address"))
	assert.Contains(t, buf.String(), "Net health:  could not determine (guest network probe returned no address)\n")
}

func TestPrintSandboxNetwork_NoNetHealthLineWhenUnprobed(t *testing.T) {
	var buf bytes.Buffer
	printSandboxNetwork(&buf, netHealthTestInfo("", ""))
	assert.NotContains(t, buf.String(), "Net health:")
}
