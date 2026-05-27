// ABOUTME: Cobra command group IDs — exported constants so every CLI subpackage
// ABOUTME: can tag its commands without depending on the root cli package.

package cliutil

// Cobra group IDs used to bucket the root command's subcommands in
// `--help` output. The corresponding group titles are registered on
// the root command in internal/cli/commands.go.
//
// These are exported because every subpackage that adds a top-level
// command (commands/, system/, profile/, configcmd/, mcp/, …) needs
// to set GroupID on its returned cobra.Command, and the alternative
// — having the root cli package set GroupID after construction —
// would split each command's metadata across two files.
const (
	GroupLifecycle    = "lifecycle"
	GroupWorkflow     = "workflow"
	GroupSandboxTools = "sandbox-tools"
	GroupAdmin        = "admin"
)
