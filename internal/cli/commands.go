// ABOUTME: Top-level command registration. Builds the help groups and wires
// ABOUTME: each subcommand constructor (defined in its own file) onto the root
// ABOUTME: cobra.Command. Individual command bodies live next to their flags.
package cli

import (
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/cli/system"
	"github.com/spf13/cobra"
)

// registerCommands adds all subcommands to the root command.
func registerCommands(root *cobra.Command, version, commit, date string) {
	root.AddGroup(
		&cobra.Group{ID: cliutil.GroupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: cliutil.GroupWorkflow, Title: "Workflow:"},
		&cobra.Group{ID: cliutil.GroupSandboxTools, Title: "Sandbox Tools:"},
		&cobra.Group{ID: cliutil.GroupAdmin, Title: "Admin:"},
	)

	root.AddCommand(
		// Lifecycle
		newNewCmd(version),
		newCloneCmd(),
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newDestroyCmd(),
		newResetCmd(),
		newMCPCmd(),

		// Workflow
		newAttachCmd(),
		newDiffCmd(),
		newApplyCmd(),
		newBaselineCmd(),
		newFilesCmd(),
		newXCmd(),

		// Sandbox Tools
		newSandboxCmd(),
		newLsAliasCmd(),
		newLogAliasCmd(),
		newExecAliasCmd(),
		newVscodeAliasCmd(),

		// Admin
		system.NewCmd(version, commit, date),
		newProfileCmd(),
		newHelpCmd(),
		newConfigCmd(),
		newVersionCmd(version, commit, date),
	)
}
