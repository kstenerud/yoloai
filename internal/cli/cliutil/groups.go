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

// AnnotationSkipMigrationGate marks a command as exempt from the startup
// migration gate (see internal/cli/root.go). Commands carrying this annotation
// must run even on an un-migrated or empty data directory: `version`, `help`,
// shell `completion`, and `system migrate` itself (which performs the
// migration). Set it as a key in a command's Annotations map with any value.
// The gate checks the flag on the invoked command and every ancestor.
const AnnotationSkipMigrationGate = "yoloai_skip_migration_gate"

// SkipMigrationGateAnnotations is the ready-made map to drop into a command's
// Annotations field to exempt it from the gate.
var SkipMigrationGateAnnotations = map[string]string{AnnotationSkipMigrationGate: "true"}
