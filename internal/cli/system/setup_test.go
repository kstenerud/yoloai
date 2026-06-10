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

	"github.com/kstenerud/yoloai/internal/cli/clitest"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// presetByID is a test helper to fetch a preset from a host's list.
func presetByID(t *testing.T, presets []envPreset, id string) envPreset {
	t.Helper()
	p, ok := findPreset(presets, id)
	require.True(t, ok, "preset %q must exist", id)
	return p
}

func TestPresetsForHost(t *testing.T) {
	mac := presetsForHost("darwin")
	require.NotEmpty(t, mac)
	assert.Equal(t, "apple", mac[0].ID, "apple is the recommended macOS default (first)")
	macIDs := joinPresetIDs(mac)
	for _, want := range []string{"apple", "orbstack", "docker-desktop", "podman", "tart", "seatbelt"} {
		assert.Contains(t, macIDs, want)
	}

	linux := presetsForHost("linux")
	assert.Equal(t, "docker", linux[0].ID, "docker leads on Linux")
	assert.Contains(t, joinPresetIDs(linux), "vm")
	assert.NotContains(t, joinPresetIDs(linux), "apple", "no apple/tart/seatbelt on Linux")

	other := presetsForHost("windows")
	assert.Equal(t, "docker", other[0].ID)
	assert.NotContains(t, joinPresetIDs(other), "vm")
}

func TestPresetConfigOps(t *testing.T) {
	mac := presetsForHost("darwin")

	// tart: writes os+isolation, resets container_backend.
	set, reset := presetConfigOps(presetByID(t, mac, "tart"))
	assert.Equal(t, map[string]string{"os": "mac", "isolation": "vm"}, set)
	assert.Equal(t, []string{"container_backend"}, reset)

	// apple: writes nothing (all defaults), resets all three.
	set, reset = presetConfigOps(presetByID(t, mac, "apple"))
	assert.Empty(t, set)
	assert.ElementsMatch(t, []string{"os", "isolation", "container_backend"}, reset)

	// orbstack: writes container_backend, resets os+isolation.
	set, reset = presetConfigOps(presetByID(t, mac, "orbstack"))
	assert.Equal(t, map[string]string{"container_backend": "orbstack"}, set)
	assert.ElementsMatch(t, []string{"os", "isolation"}, reset)
}

// TestApplyPreset_SetsAndResets is the regression for AC2: switching presets
// must CLEAR the keys the new preset doesn't own, not leave them stale (an empty
// Set would be ignored by mergeStringField).
func TestApplyPreset_SetsAndResets(t *testing.T) {
	_ = clitest.Home(t)
	ctx := context.Background()
	sc, err := cliutil.System()
	require.NoError(t, err)
	mac := presetsForHost("darwin")

	// Apply tart → os=mac, isolation=vm present.
	require.NoError(t, applyPreset(ctx, sc.Config(), presetByID(t, mac, "tart")))
	v, err := sc.Config().Get(ctx, "os")
	require.NoError(t, err)
	assert.Equal(t, "mac", v)
	v, err = sc.Config().Get(ctx, "isolation")
	require.NoError(t, err)
	assert.Equal(t, "vm", v)

	// Switch to apple → the os and isolation overrides must be cleared, i.e. no
	// longer the stale tart values (AC2). (They revert to whatever the lower
	// layers provide; the invariant under test is "not stale".)
	require.NoError(t, applyPreset(ctx, sc.Config(), presetByID(t, mac, "apple")))
	v, _ = sc.Config().Get(ctx, "os")
	assert.NotEqual(t, "mac", v, "os override must be cleared after switching to apple")
	v, _ = sc.Config().Get(ctx, "isolation")
	assert.NotEqual(t, "vm", v, "isolation override must be cleared after switching to apple")

	// Switch to orbstack → container_backend set, os/isolation still clear.
	require.NoError(t, applyPreset(ctx, sc.Config(), presetByID(t, mac, "orbstack")))
	v, err = sc.Config().Get(ctx, "container_backend")
	require.NoError(t, err)
	assert.Equal(t, "orbstack", v)
}

func TestResolvePreset_FlagValidation(t *testing.T) {
	mac := presetsForHost("darwin")
	cmd := &cobra.Command{}
	cmd.Flags().String("backend", "", "")

	// Valid preset id is accepted without prompting (System unused on this path).
	require.NoError(t, cmd.Flags().Set("backend", "tart"))
	got, err := resolvePreset(context.Background(), cmd, mkReader(""), &bytes.Buffer{}, mac, nil)
	require.NoError(t, err)
	assert.Equal(t, "tart", got)

	// Unknown id errors and lists the valid ids.
	require.NoError(t, cmd.Flags().Set("backend", "nope"))
	_, err = resolvePreset(context.Background(), cmd, mkReader(""), &bytes.Buffer{}, mac, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apple")
}

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
