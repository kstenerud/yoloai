package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// registerCommands adds all subcommands to the root command.
func registerCommands(root *cobra.Command, version, commit, date string) {
	root.AddCommand(
		newBuildCmd(),
		newNewCmd(version),
		newAttachCmd(),
		newShowCmd(),
		newDiffCmd(),
		newApplyCmd(),
		newListCmd(),
		newLogCmd(),
		newExecCmd(),
		newStopCmd(),
		newStartCmd(),
		newDestroyCmd(),
		newResetCmd(),
		newCompletionCmd(),
		newVersionCmd(version, commit, date),
	)
}

func newBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build [profile]",
		Short: "Build or rebuild Docker image(s)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("profiles not yet implemented")
			}

			homeDir, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("get home directory: %w", err)
			}
			yoloaiDir := filepath.Join(homeDir, ".yoloai")

			if err := docker.SeedResources(yoloaiDir); err != nil {
				return err
			}

			ctx := cmd.Context()
			client, err := docker.NewClient(ctx)
			if err != nil {
				return err
			}
			defer client.Close() //nolint:errcheck // best-effort cleanup

			if err := docker.BuildBaseImage(ctx, client, yoloaiDir, os.Stderr, slog.Default()); err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Base image yoloai-base built successfully")
			return err
		},
	}
}

func newNewCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new [flags] <name> [<workdir>] [-- <agent-args>...]",
		Short: "Create and start a sandbox",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse positional args considering --
			dashIdx := cmd.ArgsLenAtDash()
			var positional, passthrough []string
			if dashIdx < 0 {
				positional = args
			} else {
				positional = args[:dashIdx]
				passthrough = args[dashIdx:]
			}

			if len(positional) < 1 {
				return sandbox.NewUsageError("sandbox name is required")
			}
			if len(positional) > 2 {
				return sandbox.NewUsageError("too many positional arguments (expected <name> [<workdir>])")
			}

			name := positional[0]
			workdirArg := "."
			if len(positional) > 1 {
				workdirArg = positional[1]
			}

			prompt, _ := cmd.Flags().GetString("prompt")
			promptFile, _ := cmd.Flags().GetString("prompt-file")
			model, _ := cmd.Flags().GetString("model")
			agentName, _ := cmd.Flags().GetString("agent")
			networkNone, _ := cmd.Flags().GetBool("network-none")
			ports, _ := cmd.Flags().GetStringArray("port")
			replace, _ := cmd.Flags().GetBool("replace")
			noStart, _ := cmd.Flags().GetBool("no-start")
			yes, _ := cmd.Flags().GetBool("yes")

			ctx := cmd.Context()
			client, err := docker.NewClient(ctx)
			if err != nil {
				return err
			}
			defer client.Close() //nolint:errcheck // best-effort cleanup

			mgr := sandbox.NewManager(client, slog.Default(), cmd.ErrOrStderr())

			return mgr.Create(ctx, sandbox.CreateOptions{
				Name:        name,
				WorkdirArg:  workdirArg,
				Agent:       agentName,
				Model:       model,
				Prompt:      prompt,
				PromptFile:  promptFile,
				NetworkNone: networkNone,
				Ports:       ports,
				Replace:     replace,
				NoStart:     noStart,
				Yes:         yes,
				Passthrough: passthrough,
				Version:     version,
			})
		},
	}

	cmd.Flags().StringP("prompt", "p", "", "Prompt text for the agent")
	cmd.Flags().StringP("prompt-file", "f", "", "File containing the prompt")
	cmd.Flags().StringP("model", "m", "", "Model name or alias")
	cmd.Flags().String("agent", "claude", "Agent to use")
	cmd.Flags().Bool("network-none", false, "Disable network access")
	cmd.Flags().StringArray("port", nil, "Port mapping (host:container)")
	cmd.Flags().Bool("replace", false, "Replace existing sandbox")
	cmd.Flags().Bool("no-start", false, "Create but don't start the container")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmations")

	return cmd
}

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate shell completion script for the specified shell.

To load completions:

Bash:
  source <(yoloai completion bash)

Zsh:
  source <(yoloai completion zsh)

Fish:
  yoloai completion fish | source

PowerShell:
  yoloai completion powershell | Out-String | Invoke-Expression`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return sandbox.NewUsageError("unsupported shell: %s (valid: bash, zsh, fish, powershell)", args[0])
			}
		},
	}
}

func newVersionCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "yoloai version %s (commit: %s, built: %s)\n", version, commit, date)
			return err
		},
	}
}
