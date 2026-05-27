// ABOUTME: Tests for the system setup wizard prompts. These exercise the
// ABOUTME: interactive logic in isolation from yoloai.SystemClient.Setup
// ABOUTME: (which is itself non-interactive — see sandbox/setup_test.go).
package system

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai"
)

func mkReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func TestWizardTmuxConf_LargeAutoPicks(t *testing.T) {
	status := &yoloai.SetupStatus{TmuxClass: yoloai.TmuxConfigLarge}
	var out bytes.Buffer

	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader(""), &out, status)
	require.NoError(t, err)
	assert.Equal(t, "default+host", choice)
	assert.False(t, previewed)
	assert.Empty(t, out.String(), "TmuxConfigLarge skips the prompt entirely")
}

func TestWizardTmuxConf_NoneAnswerY(t *testing.T) {
	status := &yoloai.SetupStatus{TmuxClass: yoloai.TmuxConfigNone, DefaultTmuxConfig: "default\n"}
	var out bytes.Buffer

	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("y\n"), &out, status)
	require.NoError(t, err)
	assert.Equal(t, "default", choice)
	assert.False(t, previewed)
}

func TestWizardTmuxConf_NoneAnswerN(t *testing.T) {
	status := &yoloai.SetupStatus{TmuxClass: yoloai.TmuxConfigNone}
	var out bytes.Buffer

	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("n\n"), &out, status)
	require.NoError(t, err)
	assert.Equal(t, "none", choice)
	assert.False(t, previewed)
}

func TestWizardTmuxConf_NoneAnswerEmpty(t *testing.T) {
	status := &yoloai.SetupStatus{TmuxClass: yoloai.TmuxConfigNone}
	var out bytes.Buffer

	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("\n"), &out, status)
	require.NoError(t, err)
	assert.Equal(t, "default", choice, "empty answer defaults to Y")
	assert.False(t, previewed)
}

func TestWizardTmuxConf_SmallAnswerY(t *testing.T) {
	status := &yoloai.SetupStatus{
		TmuxClass:      yoloai.TmuxConfigSmall,
		UserTmuxConfig: "set -g mouse on\n",
	}
	var out bytes.Buffer

	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("y\n"), &out, status)
	require.NoError(t, err)
	assert.Equal(t, "default+host", choice)
	assert.False(t, previewed)
	assert.Contains(t, out.String(), "set -g mouse on", "small config should be displayed to the user")
}

func TestWizardTmuxConf_SmallAnswerN(t *testing.T) {
	status := &yoloai.SetupStatus{
		TmuxClass:      yoloai.TmuxConfigSmall,
		UserTmuxConfig: "set -g mouse on\n",
	}
	var out bytes.Buffer

	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("n\n"), &out, status)
	require.NoError(t, err)
	assert.Equal(t, "host", choice)
	assert.False(t, previewed)
}

func TestWizardTmuxConf_AnswerP_Previews(t *testing.T) {
	status := &yoloai.SetupStatus{
		TmuxClass:         yoloai.TmuxConfigNone,
		DefaultTmuxConfig: "DEFAULTS_HERE\n",
	}
	var out bytes.Buffer

	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("p\n"), &out, status)
	require.NoError(t, err)
	assert.True(t, previewed, "[p] must signal preview so caller skips Setup")
	assert.Empty(t, choice)
	assert.Contains(t, out.String(), "DEFAULTS_HERE", "[p] prints the embedded defaults")
}

func TestWizardTmuxConf_UnknownAnswerDefaultsToY(t *testing.T) {
	status := &yoloai.SetupStatus{TmuxClass: yoloai.TmuxConfigNone}
	var out bytes.Buffer

	choice, _, err := wizardTmuxConf(context.Background(), mkReader("xyz\n"), &out, status)
	require.NoError(t, err)
	assert.Equal(t, "default", choice)
}

func TestWizardChoice_PicksFirstByDefault(t *testing.T) {
	choices := []yoloai.SetupChoice{
		{Name: "docker", Blurb: "Docker"},
		{Name: "podman", Blurb: "Podman"},
	}
	var out bytes.Buffer
	got, err := wizardChoice(context.Background(), mkReader("\n"), &out, "Backend:", choices, 0)
	require.NoError(t, err)
	assert.Equal(t, "docker", got)
	assert.Contains(t, out.String(), "Backend:")
	assert.Contains(t, out.String(), "docker")
	assert.Contains(t, out.String(), "podman")
}

func TestWizardChoice_PicksByNumber(t *testing.T) {
	choices := []yoloai.SetupChoice{
		{Name: "docker", Blurb: "Docker"},
		{Name: "podman", Blurb: "Podman"},
	}
	var out bytes.Buffer
	got, err := wizardChoice(context.Background(), mkReader("2\n"), &out, "Backend:", choices, 0)
	require.NoError(t, err)
	assert.Equal(t, "podman", got)
}

func TestWizardChoice_InvalidFallsBackToDefault(t *testing.T) {
	choices := []yoloai.SetupChoice{
		{Name: "docker", Blurb: "Docker"},
		{Name: "podman", Blurb: "Podman"},
	}
	var out bytes.Buffer
	got, err := wizardChoice(context.Background(), mkReader("xyz\n"), &out, "Backend:", choices, 1)
	require.NoError(t, err)
	assert.Equal(t, "podman", got, "invalid input falls back to defaultIdx")
}

func TestDefaultAgentIdx_PrefersClaude(t *testing.T) {
	choices := []yoloai.SetupChoice{
		{Name: "aider"},
		{Name: "claude"},
		{Name: "codex"},
	}
	assert.Equal(t, 1, defaultAgentIdx(choices))
}

func TestDefaultAgentIdx_FallsBackToFirst(t *testing.T) {
	choices := []yoloai.SetupChoice{
		{Name: "aider"},
		{Name: "codex"},
	}
	assert.Equal(t, 0, defaultAgentIdx(choices))
}

func TestReadLineCtx_ReadsLine(t *testing.T) {
	line, err := readLineCtx(context.Background(), mkReader("hello\n"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", line)
}

func TestReadLineCtx_EOFReturnsEmpty(t *testing.T) {
	line, err := readLineCtx(context.Background(), mkReader(""))
	require.NoError(t, err, "EOF should not be an error — wizard treats as default")
	assert.Empty(t, line)
}
