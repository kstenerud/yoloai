package system

// ABOUTME: `yoloai system check` command — verifies prereqs for CI/CD pipelines.
// ABOUTME: Checks backend connectivity, base image, and agent credentials. Exits 1 on failure.

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

func newSystemCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Verify prerequisites (backend, image, credentials)",
		Long: `Check that yoloai prerequisites are satisfied.

Exits 0 if all checks pass, 1 if any check fails. Designed for CI/CD pipelines.

Checks performed:
  1. backend   — runtime daemon is reachable
  2. image     — yoloai-base image has been built
  3. agent     — at least one API key is set for the selected agent`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend := cliutil.ResolveBackend(cmd)
			agentName, _ := cmd.Flags().GetString("agent")
			isolation, _ := cmd.Flags().GetString("isolation")
			return runSystemCheck(cmd, backend, agentName, isolation)
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (see 'yoloai system backends')")
	cmd.Flags().String("agent", "", "Agent to check credentials for (default: configured agent)")
	cmd.Flags().String("isolation", "", "Isolation mode to validate prerequisites for (e.g. vm, vm-enhanced)")

	return cmd
}

func runSystemCheck(cmd *cobra.Command, backend yoloai.BackendName, agentName, isolation string) error {
	out := cmd.OutOrStdout()
	if agentName == "" {
		agentName = cliutil.ResolveAgent(cmd)
	}

	results, err := cliutil.NewSystemClient().Check(cmd.Context(), yoloai.CheckOptions{
		Backend:   yoloai.BackendName(backend),
		Agent:     yoloai.AgentName(agentName),
		Isolation: yoloai.IsolationMode(isolation),
	})
	if err != nil {
		return err
	}

	allOK := true
	for _, r := range results {
		if !r.OK {
			allOK = false
			break
		}
	}

	if cliutil.JSONEnabled(cmd) {
		checkJSON := make([]map[string]any, 0, len(results))
		for _, r := range results {
			entry := map[string]any{"name": r.Name, "ok": r.OK}
			if r.Message != "" {
				entry["message"] = r.Message
			}
			checkJSON = append(checkJSON, entry)
		}
		return cliutil.WriteJSON(out, map[string]any{"ok": allOK, "checks": checkJSON})
	}

	for _, r := range results {
		status := "ok"
		if !r.OK {
			status = "FAIL"
		}
		if r.Message != "" {
			fmt.Fprintf(out, "%-10s %-4s  %s\n", r.Name, status, r.Message) //nolint:errcheck
		} else {
			fmt.Fprintf(out, "%-10s %s\n", r.Name, status) //nolint:errcheck
		}
	}

	if !allOK {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}
