// ABOUTME: Tests for the built-in help/guide system: topic lookup, aliases,
// ABOUTME: unknown topic suggestions, and content loading.
package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHelpCmd_NoArgs_ShowsQuickstart(t *testing.T) {
	cmd := newHelpCmd()
	// Give it a parent with the group so GroupID validation passes.
	root := newRootCmd("test", "abc", "now")
	root.AddCommand(cmd)

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	// quickstart.md content should contain the basic workflow
	// (RunPager writes to os.Stdout, so we can't capture it directly in unit tests;
	// instead we test runHelp logic and topic resolution separately below)
}

func TestTopicLookup_Primary(t *testing.T) {
	tests := []struct {
		keyword  string
		wantFile string
	}{
		{"workflow", "workflow.md"},
		{"agents", "agents.md"},
		{"workdirs", "workdirs.md"},
		{"config", "config.md"},
		{"security", "security.md"},
		{"flags", "flags.md"},
		{"topics", "topics.md"},
	}
	for _, tt := range tests {
		t.Run(tt.keyword, func(t *testing.T) {
			got, ok := topicFile[tt.keyword]
			require.True(t, ok, "topic %q should exist", tt.keyword)
			assert.Equal(t, tt.wantFile, got)
		})
	}
}

func TestTopicLookup_Aliases(t *testing.T) {
	tests := []struct {
		alias    string
		wantFile string
	}{
		{"models", "agents.md"},
		{"directories", "workdirs.md"},
		{"configuration", "config.md"},
		{"credentials", "security.md"},
	}
	for _, tt := range tests {
		t.Run(tt.alias, func(t *testing.T) {
			got, ok := topicFile[tt.alias]
			require.True(t, ok, "alias %q should exist", tt.alias)
			assert.Equal(t, tt.wantFile, got)
		})
	}
}

func TestTopicContent_AllFilesLoadable(t *testing.T) {
	seen := make(map[string]bool)
	for _, filename := range topicFile {
		if seen[filename] {
			continue
		}
		seen[filename] = true
		t.Run(filename, func(t *testing.T) {
			content, err := helpFS.ReadFile("help/" + filename)
			require.NoError(t, err, "should be able to read %s", filename)
			assert.NotEmpty(t, content, "%s should not be empty", filename)
		})
	}
}

func TestTopicContent_QuickstartLoadable(t *testing.T) {
	content, err := helpFS.ReadFile("help/quickstart.md")
	require.NoError(t, err)
	assert.Contains(t, string(content), "QUICK START")
}

func TestUnknownTopic_Suggestions(t *testing.T) {
	err := unknownTopicError("wrkflow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Did you mean")
	assert.Contains(t, err.Error(), "workflow")
}

func TestUnknownTopic_NoSuggestion(t *testing.T) {
	err := unknownTopicError("xyzzyplugh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown help topic")
	assert.NotContains(t, err.Error(), "Did you mean")
	assert.Contains(t, err.Error(), "yoloai help topics")
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"workflow", "wrkflow", 1},
		{"config", "confg", 1},
		{"agents", "aegnts", 2},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			assert.Equal(t, tt.want, levenshtein(tt.a, tt.b))
		})
	}
}

func TestBareInvocation_ShowsIntro(t *testing.T) {
	root := newRootCmd("test", "abc", "now")
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{})
	require.NoError(t, root.Execute())

	out := buf.String()
	assert.Contains(t, out, "yoloai help")
	assert.Contains(t, out, "yoloai -h")
}
