// ABOUTME: Tests for printCreateSummary — the post-create summary the CLI now
// ABOUTME: formats from the sandbox's metadata (moved out of the library in F8).

package lifecycle

import (
	"bytes"
	"testing"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
)

func TestPrintCreateSummary_Basic(t *testing.T) {
	var buf bytes.Buffer
	printCreateSummary(&buf, &yoloai.Environment{
		Name: "test-sandbox",
		Dirs: []yoloai.DirInfo{{HostPath: "/project", Mode: "copy"}},
	}, "claude", "", "", nil, false, false)
	out := buf.String()
	assert.Contains(t, out, "test-sandbox")
	assert.Contains(t, out, "claude")
	assert.Contains(t, out, "/project")
	assert.Contains(t, out, "copy")
	assert.Contains(t, out, "attach")
}

func TestPrintCreateSummary_WithModel(t *testing.T) {
	var buf bytes.Buffer
	printCreateSummary(&buf, &yoloai.Environment{
		Name: "test-sandbox",
		Dirs: []yoloai.DirInfo{{HostPath: "/project", Mode: "copy"}},
	}, "claude", "claude-sonnet-4-6", "", nil, false, false)
	assert.Contains(t, buf.String(), "Model:    claude-sonnet-4-6")
}

func TestPrintCreateSummary_NoModelWhenUnset(t *testing.T) {
	var buf bytes.Buffer
	printCreateSummary(&buf, &yoloai.Environment{
		Name: "test-sandbox",
		Dirs: []yoloai.DirInfo{{HostPath: "/project", Mode: "copy"}},
	}, "claude", "", "", nil, false, false)
	assert.NotContains(t, buf.String(), "Model:")
}

func TestPrintCreateSummary_WithPrompt(t *testing.T) {
	var buf bytes.Buffer
	printCreateSummary(&buf, &yoloai.Environment{
		Name: "test",
		Dirs: []yoloai.DirInfo{{HostPath: "/project", Mode: "copy"}},
	}, "test", "", "", nil, true, false)
	assert.Contains(t, buf.String(), "diff", "a prompted sandbox's hint mentions 'yoloai diff'")
}

func TestPrintCreateSummary_NetworkNone(t *testing.T) {
	var buf bytes.Buffer
	printCreateSummary(&buf, &yoloai.Environment{
		Name: "test",
		Dirs: []yoloai.DirInfo{{HostPath: "/project", Mode: "copy"}},
	}, "test", "", "none", nil, false, false)
	assert.Contains(t, buf.String(), "Network:  none")
}

func TestPrintCreateSummary_NetworkIsolated(t *testing.T) {
	var buf bytes.Buffer
	printCreateSummary(&buf, &yoloai.Environment{
		Name: "test",
		Dirs: []yoloai.DirInfo{{HostPath: "/project", Mode: "copy"}},
	}, "test", "", "isolated", []string{"api.anthropic.com", "sentry.io"}, false, false)
	assert.Contains(t, buf.String(), "Network:  isolated (2 allowed domains)")
}

func TestPrintCreateSummary_WithPorts(t *testing.T) {
	var buf bytes.Buffer
	printCreateSummary(&buf, &yoloai.Environment{
		Name:  "test",
		Dirs:  []yoloai.DirInfo{{HostPath: "/project", Mode: "copy"}},
		Ports: []string{"3000:3000", "8080:80"},
	}, "test", "", "", nil, false, false)
	assert.Contains(t, buf.String(), "3000:3000")
	assert.Contains(t, buf.String(), "8080:80")
}
