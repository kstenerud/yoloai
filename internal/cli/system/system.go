package system

// ABOUTME: `yoloai system` parent command with `build` and `setup` subcommands.
// ABOUTME: Groups system-level admin operations under a single parent command.

import (
	"fmt"
	"io"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/system/tart"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

func NewCmd(version, commit, date string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "system",
		Short:   "System information and management",
		GroupID: cliutil.GroupAdmin,
	}

	cmd.AddCommand(
		newSystemInfoCmd(version, commit, date),
		newSystemAgentsCmd(),
		newSystemBackendsCmd(),
		newSystemBuildCmd(),
		newSystemCheckCmd(),
		newSystemDiskCmd(),
		newSystemMigrateCmd(),
		newSystemPruneCmd(),
		newSystemSetupCmd(),
		tart.NewCmd(cliutil.System),
		newCompletionCmd(),
	)

	return cmd
}

func newSystemBuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build [profile]",
		Short: "Build or rebuild base image (or profile image)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			backendFlag, _ := cmd.Flags().GetString("backend")

			if all && backendFlag != "" {
				return yoerrors.NewUsageError("--all and --backend are mutually exclusive")
			}

			if all {
				return runSystemBuildAll(cmd, args)
			}

			return runSystemBuild(cmd, args, cliutil.ResolveBackend(cmd))
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().StringSlice("secret", nil, "Build secret (id=<name>,src=<path>); can be repeated")
	cmd.Flags().Bool("all", false, "Build across all available backends")
	cmd.Flags().Bool("force", false, "Force rebuild even if image is up to date")

	return cmd
}

func runSystemBuild(cmd *cobra.Command, args []string, backend yoloai.BackendName) error {
	secretFlags, _ := cmd.Flags().GetStringSlice("secret")
	force, _ := cmd.Flags().GetBool("force")

	// Courtesy free-space check before pulling/building a multi-GB
	// base image. The backend's storage dir (e.g. /var/lib/docker) is
	// where the image actually lives, but enumerating it per-backend
	// is brittle; check ~/.yoloai/ as a proxy — same machine's free
	// space typically applies.
	if !cliutil.JSONEnabled(cmd) {
		cliutil.WarnIfLowDisk(cmd.ErrOrStderr(), cliutil.Layout().SandboxesDir())
	}

	var profile string
	if len(args) > 0 {
		profile = args[0]
	}

	secrets, err := prepareBuildSecrets(secretFlags, profile != "")
	if err != nil {
		return err
	}

	opts := yoloai.BuildOptions{
		Profile: profile,
		Backend: yoloai.BackendName(backend),
		Rebuild: force,
		Secrets: secrets,
		Output:  buildOutputFor(cmd),
	}
	if err := cliutil.System().Build(cmd.Context(), opts); err != nil {
		return err
	}
	return reportBuildOK(cmd, profile)
}

// prepareBuildSecrets validates --secret flags (tilde-expanding their
// src paths) and prepends auto-detected secrets. Returns *UsageError
// if --secret was used without a profile.
func prepareBuildSecrets(secretFlags []string, hasProfile bool) ([]string, error) {
	if !hasProfile && len(secretFlags) > 0 {
		return nil, yoerrors.NewUsageError("--secret is only supported with profile builds")
	}
	if !hasProfile {
		return nil, nil
	}
	homeDir := cliutil.Layout().HomeDir
	var secrets []string
	for _, s := range secretFlags {
		expanded, err := yoloai.ValidateBuildSecret(s, homeDir)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, expanded)
	}
	return append(yoloai.AutoBuildSecrets(homeDir), secrets...), nil
}

// buildOutputFor returns stderr for human mode (build stream is
// noisy; users want to see progress) and io.Discard in --json mode
// (machine-readable output mustn't be polluted by build stream).
func buildOutputFor(cmd *cobra.Command) io.Writer {
	if cliutil.JSONEnabled(cmd) {
		return io.Discard
	}
	return os.Stderr
}

// reportBuildOK prints the post-build "Built successfully" line in
// human mode and the equivalent JSON object in --json mode.
func reportBuildOK(cmd *cobra.Command, profile string) error {
	if cliutil.JSONEnabled(cmd) {
		payload := map[string]string{"action": "built"}
		if profile != "" {
			payload["profile"] = profile
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), payload)
	}
	out := cmd.OutOrStdout()
	if profile != "" {
		_, err := fmt.Fprintln(out, "Profile image built successfully")
		return err
	}
	_, err := fmt.Fprintln(out, "Base image built successfully")
	return err
}

func runSystemBuildAll(cmd *cobra.Command, args []string) error {
	if !cliutil.JSONEnabled(cmd) {
		cliutil.WarnIfLowDisk(cmd.ErrOrStderr(), cliutil.Layout().SandboxesDir())
	}

	secretFlags, _ := cmd.Flags().GetStringSlice("secret")
	force, _ := cmd.Flags().GetBool("force")

	var profile string
	if len(args) > 0 {
		profile = args[0]
	}
	secrets, err := prepareBuildSecrets(secretFlags, profile != "")
	if err != nil {
		return err
	}

	opts := yoloai.BuildOptions{
		Profile:     profile,
		AllBackends: true,
		Rebuild:     force,
		Secrets:     secrets,
		Output:      buildOutputFor(cmd),
	}
	if err := cliutil.System().Build(cmd.Context(), opts); err != nil {
		// System.Build returns "no available backends to build
		// for" — preserve the original CLI behavior of printing the
		// message and exiting 0 in that case.
		if err.Error() == "no available backends to build for" {
			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{"action": "built", "backends": []string{}})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "No available backends to build for.") //nolint:errcheck
			return nil
		}
		return err
	}
	return nil
}

func newSystemSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Run interactive setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSystemSetup(cmd)
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().String("agent", "", "Default agent (skip prompt)")
	cmd.Flags().String("tmux-conf", "", "Tmux config mode: default, default+host, host, none (skip prompt)")

	return cmd
}
