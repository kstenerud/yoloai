// ABOUTME: Top-level command registration. Builds the help groups and wires
// ABOUTME: each subcommand constructor (defined in its own file) onto the root
// ABOUTME: cobra.Command. Individual command bodies live next to their flags.
package cli

import (
	"github.com/spf13/cobra"
)

// Command group IDs for help output.
const (
	groupLifecycle    = "lifecycle"
	groupWorkflow     = "workflow"
	groupSandboxTools = "sandbox-tools"
	groupAdmin        = "admin"
)

// registerCommands adds all subcommands to the root command.
func registerCommands(root *cobra.Command, version, commit, date string) {
	root.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle:"},
		&cobra.Group{ID: groupWorkflow, Title: "Workflow:"},
		&cobra.Group{ID: groupSandboxTools, Title: "Sandbox Tools:"},
		&cobra.Group{ID: groupAdmin, Title: "Admin:"},
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
		newSystemCmd(version, commit, date),
		newProfileCmd(),
		newHelpCmd(),
		newConfigCmd(),
		newVersionCmd(version, commit, date),
	)
}
