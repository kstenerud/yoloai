// ABOUTME: Tests for the `yoloai system setup` wizard. The wizard now owns all
// ABOUTME: host inspection, prompting, and auto-pick; it writes answers via the
// ABOUTME: public Config().Set verb (the library has no setup-wizard surface).
package system

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func TestWizardTmuxConf_LargeAutoPicks(t *testing.T) {
	var out bytes.Buffer
	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader(""), &out, tmuxLarge, "")
	require.NoError(t, err)
	assert.Equal(t, "default+host", choice)
	assert.False(t, previewed)
	assert.Empty(t, out.String(), "tmuxLarge skips the prompt entirely")
}

func TestWizardTmuxConf_NoneAnswerY(t *testing.T) {
	var out bytes.Buffer
	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("y\n"), &out, tmuxNone, "")
	require.NoError(t, err)
	assert.Equal(t, "default", choice)
	assert.False(t, previewed)
}

func TestWizardTmuxConf_NoneAnswerN(t *testing.T) {
	var out bytes.Buffer
	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("n\n"), &out, tmuxNone, "")
	require.NoError(t, err)
	assert.Equal(t, "none", choice)
	assert.False(t, previewed)
}

func TestWizardTmuxConf_NoneAnswerEmpty(t *testing.T) {
	var out bytes.Buffer
	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("\n"), &out, tmuxNone, "")
	require.NoError(t, err)
	assert.Equal(t, "default", choice, "empty answer defaults to Y")
	assert.False(t, previewed)
}

func TestWizardTmuxConf_SmallAnswerY(t *testing.T) {
	var out bytes.Buffer
	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("y\n"), &out, tmuxSmall, "set -g mouse on\n")
	require.NoError(t, err)
	assert.Equal(t, "default+host", choice)
	assert.False(t, previewed)
	assert.Contains(t, out.String(), "set -g mouse on", "small config should be displayed to the user")
}

func TestWizardTmuxConf_SmallAnswerN(t *testing.T) {
	var out bytes.Buffer
	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("n\n"), &out, tmuxSmall, "set -g mouse on\n")
	require.NoError(t, err)
	assert.Equal(t, "host", choice)
	assert.False(t, previewed)
}

func TestWizardTmuxConf_AnswerP_Previews(t *testing.T) {
	var out bytes.Buffer
	choice, previewed, err := wizardTmuxConf(context.Background(), mkReader("p\n"), &out, tmuxNone, "")
	require.NoError(t, err)
	assert.True(t, previewed, "[p] must signal preview so caller skips writing config")
	assert.Empty(t, choice)
	assert.Contains(t, out.String(), "--- yoloai defaults ---", "[p] prints the embedded defaults")
}

func TestWizardTmuxConf_UnknownAnswerDefaultsToY(t *testing.T) {
	var out bytes.Buffer
	choice, _, err := wizardTmuxConf(context.Background(), mkReader("xyz\n"), &out, tmuxNone, "")
	require.NoError(t, err)
	assert.Equal(t, "default", choice)
}

func TestWizardChoice_PicksFirstByDefault(t *testing.T) {
	choices := []setupChoice{
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
	choices := []setupChoice{
		{Name: "docker", Blurb: "Docker"},
		{Name: "podman", Blurb: "Podman"},
	}
	var out bytes.Buffer
	got, err := wizardChoice(context.Background(), mkReader("2\n"), &out, "Backend:", choices, 0)
	require.NoError(t, err)
	assert.Equal(t, "podman", got)
}

func TestWizardChoice_InvalidFallsBackToDefault(t *testing.T) {
	choices := []setupChoice{
		{Name: "docker", Blurb: "Docker"},
		{Name: "podman", Blurb: "Podman"},
	}
	var out bytes.Buffer
	got, err := wizardChoice(context.Background(), mkReader("xyz\n"), &out, "Backend:", choices, 1)
	require.NoError(t, err)
	assert.Equal(t, "podman", got, "invalid input falls back to defaultIdx")
}

func TestDefaultAgentIdx_PrefersClaude(t *testing.T) {
	choices := []setupChoice{{Name: "aider"}, {Name: "claude"}, {Name: "codex"}}
	assert.Equal(t, 1, defaultAgentIdx(choices))
}

func TestDefaultAgentIdx_FallsBackToFirst(t *testing.T) {
	choices := []setupChoice{{Name: "aider"}, {Name: "codex"}}
	assert.Equal(t, 0, defaultAgentIdx(choices))
}

func TestClassifyTmuxConfig(t *testing.T) {
	t.Run("missing file is tmuxNone", func(t *testing.T) {
		class, content := classifyTmuxConfig(t.TempDir())
		assert.Equal(t, tmuxNone, class)
		assert.Empty(t, content)
	})

	t.Run("few significant lines is tmuxSmall", func(t *testing.T) {
		home := t.TempDir()
		body := "# a comment\n\nset -g mouse on\n"
		require.NoError(t, os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(body), 0o600))
		class, content := classifyTmuxConfig(home)
		assert.Equal(t, tmuxSmall, class)
		assert.Equal(t, body, content)
	})

	t.Run("many significant lines is tmuxLarge", func(t *testing.T) {
		home := t.TempDir()
		var b strings.Builder
		for i := 0; i <= significantLineThreshold; i++ {
			b.WriteString("set -g foo bar\n")
		}
		require.NoError(t, os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(b.String()), 0o600))
		class, _ := classifyTmuxConfig(home)
		assert.Equal(t, tmuxLarge, class)
	})
}

func TestResolveChoice(t *testing.T) {
	choices := []setupChoice{{Name: "docker"}, {Name: "podman"}}

	t.Run("valid flag is accepted", func(t *testing.T) {
		var out bytes.Buffer
		got, err := resolveChoice(context.Background(), mkReader(""), &out, "backend", "podman", choices, "Backend:", 0)
		require.NoError(t, err)
		assert.Equal(t, "podman", got)
	})

	t.Run("invalid flag errors", func(t *testing.T) {
		var out bytes.Buffer
		_, err := resolveChoice(context.Background(), mkReader(""), &out, "backend", "nope", choices, "Backend:", 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "docker, podman")
	})

	t.Run("single choice auto-picks without prompting", func(t *testing.T) {
		var out bytes.Buffer
		got, err := resolveChoice(context.Background(), mkReader(""), &out, "backend", "", []setupChoice{{Name: "docker"}}, "Backend:", 0)
		require.NoError(t, err)
		assert.Equal(t, "docker", got)
		assert.Empty(t, out.String(), "single available choice should not prompt")
	})

	t.Run("zero choices returns empty", func(t *testing.T) {
		var out bytes.Buffer
		got, err := resolveChoice(context.Background(), mkReader(""), &out, "backend", "", nil, "Backend:", 0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
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
