package cli

// ABOUTME: Built-in help/guide system. Provides keyword-based topic guides
// ABOUTME: via embedded markdown files with fuzzy suggestion for unknown topics.

import (
	"embed"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/spf13/cobra"
)

//go:embed help/*.md
var helpFS embed.FS

// topicFile maps a topic keyword to its embedded markdown filename.
var topicFile = map[string]string{
	"topics":        "topics.md",
	"workflow":      "workflow.md",
	"workdirs":      "workdirs.md",
	"directories":   "workdirs.md",
	"config":        "config.md",
	"configuration": "config.md",
	"security":      "security.md",
	"credentials":   "security.md",
	"flags":         "flags.md",
	"extensions":    "extensions.md",
	"x":             "extensions.md",
	"ext":           "extensions.md",
	"vscode-tunnel": "vscode-tunnel.md",
	"tunnel":        "vscode-tunnel.md",
	"remote":        "vscode-tunnel.md",
}

// topicFunc maps a topic keyword to a function that generates content
// dynamically. Checked before topicFile so generated topics stay in sync
// with the code.
var topicFunc = map[string]func() string{
	"agents": generateAgentsTopic,
	"models": generateAgentsTopic,
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
	if topic != "" {
		// Check dynamic topics first.
		if fn, ok := topicFunc[topic]; ok {
			_, err := os.Stdout.WriteString(fn())
			return err
		}

		f, ok := topicFile[topic]
		if !ok {
			return unknownTopicError(topic)
		}
		content, err := helpFS.ReadFile("help/" + f)
		if err != nil {
			return fmt.Errorf("reading help topic: %w", err)
		}
		_, err = os.Stdout.Write(content)
		return err
	}

	content, err := helpFS.ReadFile("help/quickstart.md")
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

// allTopicKeywords returns the union of topicFile and topicFunc keys.
func allTopicKeywords() []string {
	seen := make(map[string]bool)
	for k := range topicFile {
		seen[k] = true
	}
	for k := range topicFunc {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// unknownTopicError returns an error with suggestions for unknown topics.
func unknownTopicError(topic string) error {
	var suggestions []string
	for _, keyword := range allTopicKeywords() {
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

// generateAgentsTopic builds the "agents" help topic dynamically from agent
// definitions so it stays in sync with the code.
func generateAgentsTopic() string {
	var b strings.Builder
	realAgents := agent.RealAgents()

	b.WriteString("AGENTS AND MODELS\n")
	b.WriteString("\n")
	b.WriteString("  yoloai ships multiple agents. Select with --agent or set a default:\n")
	b.WriteString("\n")
	b.WriteString("     yoloai new task . --agent gemini\n")
	b.WriteString("     yoloai config set agent gemini\n")
	b.WriteString("\n")
	b.WriteString("AVAILABLE AGENTS\n")
	b.WriteString("\n")
	writeAgentList(&b, realAgents)

	b.WriteString("\n")
	b.WriteString("  Agent details:   yoloai system agents <name>\n")
	b.WriteString("\n")
	b.WriteString("MODEL ALIASES\n")
	b.WriteString("\n")
	b.WriteString("  Each agent supports shorthand model aliases with --model.\n")
	b.WriteString("  Run 'yoloai system agents <name>' for the full list.\n")
	writeAgentAliases(&b, realAgents)

	b.WriteString("\n")
	b.WriteString("  Set a default model:\n")
	b.WriteString("\n")
	b.WriteString("     yoloai config set model sonnet\n")
	b.WriteString("\n")
	b.WriteString("LOCAL MODELS\n")
	b.WriteString("\n")
	b.WriteString("  Some agents (e.g. aider) support local model servers (Ollama, LM Studio):\n")
	b.WriteString("\n")
	b.WriteString("     yoloai config set env.OLLAMA_API_BASE \\\n")
	fmt.Fprintf(&b, "       http://%s:11434\n", containerHostExample()) //nolint:errcheck
	b.WriteString("\n")
	b.WriteString("More info: https://github.com/kstenerud/yoloai/blob/main/docs/GUIDE.md#agents-and-models\n")

	return b.String()
}

// containerHostExample returns a hostname for the LOCAL MODELS example —
// the first non-empty HostFromContainer found among registered backends,
// or "<your-host-ip>" when no backend declares one. Iterating descriptors
// keeps the example honest if a new container backend ships without
// docker's host.docker.internal convention.
func containerHostExample() string {
	for _, desc := range runtime.Descriptors() {
		if desc.HostFromContainer != "" {
			return desc.HostFromContainer
		}
	}
	return "<your-host-ip>"
}

// writeAgentList writes the AVAILABLE AGENTS section to b.
func writeAgentList(b *strings.Builder, realAgents []string) {
	maxName := 0
	for _, name := range realAgents {
		if len(name) > maxName {
			maxName = len(name)
		}
	}
	labelWidth := maxName + len(" (default)")

	for _, name := range realAgents {
		def := agent.GetAgent(name)
		label := name
		if name == "claude" {
			label = name + " (default)"
		}
		keys := ""
		if len(def.APIKeyEnvVars) > 0 {
			keys = "Requires " + def.APIKeyEnvVars[0]
		}
		fmt.Fprintf(b, "  %-*s  %s", labelWidth, label, def.Description)
		if keys != "" {
			fmt.Fprintf(b, "\n  %-*s  %s", labelWidth, "", keys)
		}
		b.WriteString("\n")
	}
}

// writeAgentAliases writes the per-agent model alias tables to b.
func writeAgentAliases(b *strings.Builder, realAgents []string) {
	for _, name := range realAgents {
		def := agent.GetAgent(name)
		if len(def.ModelAliases) == 0 {
			continue
		}
		title := strings.ToUpper(name[:1]) + name[1:]
		fmt.Fprintf(b, "\n  %s:\n", title)

		aliases := make([]string, 0, len(def.ModelAliases))
		for alias := range def.ModelAliases {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)

		maxAlias := 0
		for _, alias := range aliases {
			if len(alias) > maxAlias {
				maxAlias = len(alias)
			}
		}
		for _, alias := range aliases {
			fmt.Fprintf(b, "     %-*s → %s\n", maxAlias, alias, def.ModelAliases[alias])
		}
	}
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
