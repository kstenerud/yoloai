package cli

// ABOUTME: `yoloai system doctor` command — shows what backends and isolation modes
// ABOUTME: are available on the current machine, with fix instructions for missing prerequisites.

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kstenerud/yoloai/runtime/caps"
)

// checkResultJSON is the JSON-serializable form of a single capability check result.
type checkResultJSON struct {
	CapID       string         `json:"cap_id"`
	CapSummary  string         `json:"cap_summary"`
	OK          bool           `json:"ok"`
	IsPermanent bool           `json:"is_permanent,omitempty"`
	Error       string         `json:"error,omitempty"`
	Steps       []caps.FixStep `json:"steps,omitempty"`
}

// backendReportJSON is the JSON-serializable form of a BackendReport.
type backendReportJSON struct {
	Backend      string            `json:"backend"`
	Mode         string            `json:"mode"`
	IsBaseMode   bool              `json:"is_base_mode"`
	Availability string            `json:"availability"`
	InitError    string            `json:"init_error,omitempty"`
	Checks       []checkResultJSON `json:"checks,omitempty"`
}

func newSystemDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Show capability status for all backends and isolation modes",
		Long: `Check which backends and isolation modes are available on this machine.

Shows three tiers:
  Ready to use     — all prerequisites satisfied
  Needs setup      — prerequisites missing but fixable
  Not available    — hardware or OS mismatch (not actionable)

Exit code 0 if no NeedsSetup entries; 1 if any NeedsSetup entries exist.
Unavailable entries do not affect the exit code.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backendFilter, _ := cmd.Flags().GetString("backend")
			isolationFilter, _ := cmd.Flags().GetString("isolation")
			isJSON := jsonEnabled(cmd)
			return runSystemDoctor(cmd, backendFilter, isolationFilter, isJSON)
		},
	}

	cmd.Flags().String("backend", "", "Check only this backend")
	cmd.Flags().String("isolation", "", "Check only this isolation mode")

	return cmd
}

func runSystemDoctor(cmd *cobra.Command, backendFilter, isolationFilter string, isJSON bool) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	env := caps.DetectEnvironment()
	var reports []caps.BackendReport

	for _, b := range knownBackends {
		if backendFilter != "" && b.Name != backendFilter {
			continue
		}

		rt, err := newRuntime(ctx, b.Name)
		if err != nil {
			// Backend unavailable — only include if no isolation filter, or we can't know.
			if isolationFilter == "" {
				reports = append(reports, caps.BackendReport{
					Backend:      b.Name,
					Mode:         "?",
					IsBaseMode:   true,
					InitErr:      err,
					Availability: caps.Unavailable,
				})
			}
			continue
		}

		// Base mode report (no isolation filter, or no isolation filter mismatch).
		if isolationFilter == "" {
			reports = append(reports, caps.BackendReport{
				Backend:      b.Name,
				Mode:         rt.BaseModeName(),
				IsBaseMode:   true,
				Availability: caps.Ready, // New() succeeded → base mode is Ready
			})
		}

		// Isolation mode reports.
		for _, mode := range rt.SupportedIsolationModes() {
			if isolationFilter != "" && mode != isolationFilter {
				continue
			}
			capList := rt.RequiredCapabilities(mode)
			results := caps.RunChecks(ctx, capList, env)
			avail := caps.ComputeAvailability(results)
			reports = append(reports, caps.BackendReport{
				Backend:      b.Name,
				Mode:         mode,
				IsBaseMode:   false,
				Results:      results,
				Availability: avail,
			})
		}

		_ = rt.Close() //nolint:errcheck,gosec // best-effort cleanup
	}

	if isJSON {
		jsonReports := make([]backendReportJSON, 0, len(reports))
		for _, r := range reports {
			jr := backendReportJSON{
				Backend:    r.Backend,
				Mode:       r.Mode,
				IsBaseMode: r.IsBaseMode,
			}
			switch r.Availability {
			case caps.Ready:
				jr.Availability = "ready"
			case caps.NeedsSetup:
				jr.Availability = "needs_setup"
			default:
				jr.Availability = "unavailable"
			}
			if r.InitErr != nil {
				jr.InitError = r.InitErr.Error()
			}
			for _, cr := range r.Results {
				jcr := checkResultJSON{
					CapID:      cr.Cap.ID,
					CapSummary: cr.Cap.Summary,
					OK:         cr.Err == nil,
				}
				if cr.Err != nil {
					jcr.IsPermanent = cr.IsPermanent
					jcr.Error = cr.Err.Error()
					jcr.Steps = cr.Steps
				}
				jr.Checks = append(jr.Checks, jcr)
			}
			jsonReports = append(jsonReports, jr)
		}
		return writeJSON(out, jsonReports)
	}

	caps.FormatDoctor(out, reports)

	// Exit 1 if any NeedsSetup entries (user action could unlock them).
	// Unavailable entries do not cause exit 1 — they are not actionable.
	for _, r := range reports {
		if r.Availability == caps.NeedsSetup {
			return fmt.Errorf("one or more backends need setup")
		}
	}
	return nil
}
