// ABOUTME: Tests for the built-in help/guide system: topic lookup, aliases,
// ABOUTME: unknown topic suggestions, and content loading. These touch
// ABOUTME: package-private state so they live in package helpcmd.
package helpcmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopicLookup_Primary(t *testing.T) {
	tests := []struct {
		keyword  string
		wantFile string
	}{
		{"workflow", "workflow.md"},
		{"workdirs", "workdirs.md"},
		{"config", "config.md"},
		{"security", "security.md"},
		{"flags", "flags.md"},
		{"topics", "topics.md"},
		{"cleanup", "cleanup.md"},
	}
	for _, tt := range tests {
		t.Run(tt.keyword, func(t *testing.T) {
			got, ok := topicFile[tt.keyword]
			require.True(t, ok, "topic %q should exist", tt.keyword)
			assert.Equal(t, tt.wantFile, got)
		})
	}
}

func TestTopicLookup_DynamicTopics(t *testing.T) {
	for _, keyword := range []string{"agents", "models"} {
		t.Run(keyword, func(t *testing.T) {
			fn, ok := topicFunc[keyword]
			require.True(t, ok, "topic %q should have a dynamic generator", keyword)
			content := fn()
			assert.Contains(t, content, "AVAILABLE AGENTS")
			assert.Contains(t, content, "MODEL ALIASES")
		})
	}
}

func TestTopicLookup_Aliases(t *testing.T) {
	tests := []struct {
		alias    string
		wantFile string
	}{
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
