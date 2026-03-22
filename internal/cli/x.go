package cli

// ABOUTME: CLI commands for yoloai x (extensions). Loads user-defined extension
// ABOUTME: YAML files and registers them as dynamic Cobra subcommands.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/extension"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newXCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "x [extension] [args...] [--flags...]",
		Short:   "Run a user-defined extension",
		Long:    "Run user-defined extensions from ~/.yoloai/extensions/.\nRun without arguments to list available extensions.\n\nSee 'yoloai help extensions' for how to create and install extensions.",
		GroupID: groupWorkflow,
		Aliases: []string{"ext"},
		RunE:    runExtensionList,
	}

	registerExtensionSubcommands(cmd)

	return cmd
}

// registerExtensionSubcommands loads all extensions and adds them as
// dynamic subcommands of the parent command.
func registerExtensionSubcommands(parent *cobra.Command) {
	exts, err := extension.LoadAll()
	if err != nil {
		slog.Debug("failed to load extensions", "event", "extension.load_error", "error", err)
		return
	}

	for _, ext := range exts {
		if err := extension.Validate(ext); err != nil {
			slog.Debug("skipping invalid extension", "event", "extension.skip", "name", ext.Name, "error", err)
			continue
		}
		parent.AddCommand(newExtensionRunCmd(ext))
	}
}

// newExtensionRunCmd creates a Cobra command for a single extension.
func newExtensionRunCmd(ext *extension.Extension) *cobra.Command {
	// Build Use string with arg placeholders
	use := ext.Name
	for _, a := range ext.Args {
		use += " <" + a.Name + ">"
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: ext.Description,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExtension(cmd, ext, args)
		},
	}

	// Register flags from extension definition
	for _, f := range ext.Flags {
		if f.Short != "" {
			cmd.Flags().StringP(f.Name, f.Short, f.Default, f.Description)
		} else {
			cmd.Flags().String(f.Name, f.Default, f.Description)
		}
	}

	return cmd
}

// runExtension validates args, builds the environment, and executes the action script.
func runExtension(cmd *cobra.Command, ext *extension.Extension, args []string) error {
	// Validate arg count
	if len(args) < len(ext.Args) {
		var expected []string
		for _, a := range ext.Args {
			expected = append(expected, "<"+a.Name+">")
		}
		return sandbox.NewUsageError("expected %d argument(s): %s", len(ext.Args), strings.Join(expected, " "))
	}
	if len(args) > len(ext.Args) {
		return sandbox.NewUsageError("expected %d argument(s) but got %d", len(ext.Args), len(args))
	}

	// Resolve and validate agent
	agentName := resolveAgentFromConfig()
	if !ext.SupportsAgent(agentName) {
		return sandbox.NewUsageError("extension %q does not support agent %q (supports: %s)",
			ext.Name, agentName, strings.Join(ext.Agent.Names, ", "))
	}

	// Build environment
	env := os.Environ()
	env = append(env, "agent="+agentName)

	for i, a := range ext.Args {
		env = append(env, a.Name+"="+args[i])
	}

	for _, f := range ext.Flags {
		val, _ := cmd.Flags().GetString(f.Name)
		envName := strings.ReplaceAll(f.Name, "-", "_")
		env = append(env, envName+"="+val)
	}

	// Execute action via sh -c
	sh := exec.CommandContext(cmd.Context(), "sh", "-c", ext.Action) //nolint:gosec // user-authored script
	sh.Env = env
	sh.Stdin = cmd.InOrStdin()
	sh.Stdout = cmd.OutOrStdout()
	sh.Stderr = cmd.ErrOrStderr()

	if err := sh.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &extension.ExitError{Code: exitErr.ExitCode()}
		}
		return fmt.Errorf("run extension %q: %w", ext.Name, err)
	}

	return nil
}

// runExtensionList prints available extensions as a table or JSON.
func runExtensionList(cmd *cobra.Command, _ []string) error {
	exts, err := extension.LoadAll()
	if err != nil {
		return err
	}

	if len(exts) == 0 {
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), []any{})
		}
		fmt.Fprintln(cmd.OutOrStdout(), "No extensions found.")                                                   //nolint:errcheck
		fmt.Fprintln(cmd.OutOrStdout())                                                                           //nolint:errcheck
		fmt.Fprintf(cmd.OutOrStdout(), "Add YAML files to %s to create extensions.\n", extension.ExtensionsDir()) //nolint:errcheck
		fmt.Fprintln(cmd.OutOrStdout(), "See 'yoloai help extensions' for how to create and install extensions.") //nolint:errcheck
		return nil
	}

	if jsonEnabled(cmd) {
		type jsonExt struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Agents      []string `json:"agents,omitempty"`
		}
		var out []jsonExt
		for _, ext := range exts {
			e := jsonExt{
				Name:        ext.Name,
				Description: ext.Description,
			}
			if ext.Agent != nil {
				e.Agents = ext.Agent.Names
			}
			out = append(out, e)
		}
		return writeJSON(cmd.OutOrStdout(), out)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tAGENT") //nolint:errcheck
	for _, ext := range exts {
		agentStr := "any"
		if ext.Agent != nil {
			agentStr = strings.Join(ext.Agent.Names, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", ext.Name, ext.Description, agentStr) //nolint:errcheck
	}
	return w.Flush()
}
