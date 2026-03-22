package cli

// ABOUTME: `yoloai system check` command — verifies prereqs for CI/CD pipelines.
// ABOUTME: Checks backend connectivity, base image, and agent credentials. Exits 1 on failure.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
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
			backend := resolveBackend(cmd)
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

// checkResult holds the outcome of a single check.
type checkResult struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func runSystemCheck(cmd *cobra.Command, backend, agentName, isolation string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	isJSON := jsonEnabled(cmd)

	var results []checkResult
	allOK := true

	// 1. Backend connectivity.
	{
		available, note := checkBackend(ctx, backend)
		msg := ""
		if !available {
			msg = note
			allOK = false
		}
		results = append(results, checkResult{
			Name:    "backend",
			OK:      available,
			Message: msg,
		})
	}

	// 2. Base image exists (only meaningful if backend is reachable).
	{
		r := checkResult{Name: "image"}
		err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
			exists, err := rt.IsReady(ctx)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("yoloai-base image not found — run 'yoloai system build'")
			}
			return nil
		})
		if err != nil {
			r.OK = false
			r.Message = err.Error()
			allOK = false
		} else {
			r.OK = true
		}
		results = append(results, r)
	}

	// 3. Agent credentials.
	{
		r := checkResult{Name: "agent"}
		if agentName == "" {
			agentName = resolveAgent(cmd)
		}
		def := agent.GetAgent(agentName)
		switch {
		case def == nil:
			r.OK = false
			r.Message = fmt.Sprintf("unknown agent %q", agentName)
			allOK = false
		case len(def.APIKeyEnvVars) == 0:
			// Agent needs no credentials (e.g. shell, test).
			r.OK = true
			r.Message = fmt.Sprintf("agent %q requires no credentials", agentName)
		default:
			var found []string
			for _, key := range def.APIKeyEnvVars {
				if os.Getenv(key) != "" {
					found = append(found, key)
				}
			}
			if len(found) == 0 {
				r.OK = false
				r.Message = fmt.Sprintf("no credentials set for agent %q (need one of: %s)",
					agentName, strings.Join(def.APIKeyEnvVars, ", "))
				allOK = false
			} else {
				r.OK = true
				r.Message = fmt.Sprintf("found: %s", strings.Join(found, ", "))
			}
		}
		results = append(results, r)
	}

	// 4. Isolation prerequisites (only when --isolation is specified).
	if isolation != "" {
		r := checkResult{Name: "isolation"}
		err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
			capList := rt.RequiredCapabilities(isolation)
			if len(capList) == 0 {
				return nil // backend has no requirements for this mode
			}
			env := caps.DetectEnvironment()
			results := caps.RunChecks(ctx, capList, env)
			return caps.FormatError(results)
		})
		if err != nil {
			r.OK = false
			r.Message = err.Error()
			allOK = false
		} else {
			r.OK = true
		}
		results = append(results, r)
	}

	if isJSON {
		return writeJSON(out, map[string]any{
			"ok":     allOK,
			"checks": results,
		})
	}

	// Human-readable table.
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
