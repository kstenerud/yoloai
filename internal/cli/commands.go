// ABOUTME: Top-level command registration. Builds the help groups and wires
// ABOUTME: each subcommand constructor (defined in its own file) onto the root
// ABOUTME: cobra.Command. Individual command bodies live next to their flags.
package cli

import (
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/cli/configcmd"
	"github.com/kstenerud/yoloai/internal/cli/lifecycle"
	"github.com/kstenerud/yoloai/internal/cli/mcp"
	"github.com/kstenerud/yoloai/internal/cli/profile"
	"github.com/kstenerud/yoloai/internal/cli/sandboxcmd"
	"github.com/kstenerud/yoloai/internal/cli/system"
	"github.com/kstenerud/yoloai/internal/cli/workflow"
	"github.com/kstenerud/yoloai/internal/cli/xcmd"
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
		lifecycle.NewNewCmd(version),
		lifecycle.NewCloneCmd(),
		lifecycle.NewStartCmd(),
		lifecycle.NewStopCmd(),
		lifecycle.NewRestartCmd(),
		lifecycle.NewDestroyCmd(),
		lifecycle.NewResetCmd(),
		mcp.NewCmd(),

		// Workflow
		workflow.NewAttachCmd(),
		workflow.NewDiffCmd(),
		workflow.NewApplyCmd(),
		workflow.NewBaselineCmd(),
		workflow.NewFilesCmd(),
		xcmd.NewCmd(),

		// Sandbox Tools
		sandboxcmd.NewSandboxCmd(),
		sandboxcmd.NewLsAliasCmd(),
		sandboxcmd.NewLogAliasCmd(),
		sandboxcmd.NewExecAliasCmd(),
		sandboxcmd.NewVscodeAliasCmd(),

		// Admin
		system.NewCmd(version, commit, date),
		profile.NewCmd(),
		newHelpCmd(),
		configcmd.NewCmd(),
		newVersionCmd(version, commit, date),
	)
}
