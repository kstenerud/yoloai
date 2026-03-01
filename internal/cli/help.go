package cli

// ABOUTME: Built-in help/guide system. Provides keyword-based topic guides
// ABOUTME: via embedded markdown files with fuzzy suggestion for unknown topics.

import (
	"embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

//go:embed help/*.md
var helpFS embed.FS

// topicFile maps a topic keyword to its embedded markdown filename.
var topicFile = map[string]string{
	"topics":        "topics.md",
	"workflow":      "workflow.md",
	"agents":        "agents.md",
	"models":        "agents.md",
	"workdirs":      "workdirs.md",
	"directories":   "workdirs.md",
	"config":        "config.md",
	"configuration": "config.md",
	"security":      "security.md",
	"credentials":   "security.md",
	"flags":         "flags.md",
}

func newHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "help [topic]",
		Short:   "Show help guides (run 'help topics' to list all)",
		GroupID: groupAdmin,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			topic := ""
			if len(args) > 0 {
				topic = strings.ToLower(args[0])
			}
			return runHelp(topic)
		},
	}
}

// runHelp displays the guide for the given topic, or the quickstart guide
// if no topic is specified.
func runHelp(topic string) error {
	filename := "quickstart.md"
	if topic != "" {
		f, ok := topicFile[topic]
		if !ok {
			return unknownTopicError(topic)
		}
		filename = f
	}

	content, err := helpFS.ReadFile("help/" + filename)
	if err != nil {
		return fmt.Errorf("reading help topic: %w", err)
	}

	_, err = os.Stdout.Write(content)
	return err
}

// topicError is a user-facing error with formatted help suggestions.
type topicError struct {
	msg string
}

func (e *topicError) Error() string { return e.msg }

// unknownTopicError returns an error with suggestions for unknown topics.
func unknownTopicError(topic string) error {
	var suggestions []string
	for keyword := range topicFile {
		if levenshtein(topic, keyword) <= 3 {
			suggestions = append(suggestions, keyword)
		}
	}
	sort.Strings(suggestions)

	var msg string
	if len(suggestions) > 0 {
		msg = fmt.Sprintf("unknown help topic %q\n\nDid you mean: %s?\n\nRun 'yoloai help topics' to list all topics.",
			topic, strings.Join(suggestions, ", "))
	} else {
		msg = fmt.Sprintf("unknown help topic %q\n\nRun 'yoloai help topics' to list all topics.", topic)
	}

	return &topicError{msg: msg}
}

// levenshtein computes the Levenshtein distance between two strings.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}
