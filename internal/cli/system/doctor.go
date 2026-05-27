package system

// ABOUTME: `yoloai system doctor` command — shows what backends and isolation modes
// ABOUTME: are available on the current machine, with fix instructions for missing prerequisites.

import (
	"context"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
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

// orphanItemJSON is one orphan entry — a yoloai-prefixed resource
// (container, image, snapshot, …) for which no matching sandbox dir
// exists on disk. Surfaced by doctor so users can spot accumulated
// state from crashed runs without waiting for disk-full / CPU-hot
// to make the leak visible.
type orphanItemJSON struct {
	Backend string `json:"backend"`
	Kind    string `json:"kind"` // e.g. "container", "image", "snapshot"
	Name    string `json:"name"`
}

// doctorReportJSON wraps the backend reports plus the orphan-state
// section so --json output is a single document rather than two
// disjoint streams.
type doctorReportJSON struct {
	Backends []backendReportJSON `json:"backends"`
	Orphans  []orphanItemJSON    `json:"orphans"`
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
			isJSON := cliutil.JSONEnabled(cmd)
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
	reports := collectDoctorReports(ctx, env, backendFilter, isolationFilter)

	// Orphan state: yoloai-prefixed backend resources with no matching
	// sandbox dir. A crashed destroy or wedged Kata shim can leave
	// these behind — typically invisible until disk pressure or
	// background CPU pulls the user's attention. doctor surfaces them
	// once so the user can run `yoloai system prune` without first
	// having to know orphans are a thing.
	orphans := collectOrphans(cmd)

	if isJSON {
		return cliutil.WriteJSON(out, doctorReportJSON{
			Backends: convertDoctorReportsToJSON(reports),
			Orphans:  orphans,
		})
	}

	caps.FormatDoctor(out, reports)
	renderOrphans(out, orphans)

	// Exit 1 if any NeedsSetup entries (user action could unlock them).
	// Unavailable entries do not cause exit 1 — they are not actionable.
	// Orphan state does NOT trigger exit 1: it's an advisory, not a
	// prerequisite blocker.
	for _, r := range reports {
		if r.Availability == caps.NeedsSetup {
			return fmt.Errorf("one or more backends need setup")
		}
	}
	return nil
}

// collectOrphans runs a dry-run prune across every registered
// backend and projects the result into the doctor's flat
// orphan-item shape. Errors from individual backends are silently
// dropped — doctor is a best-effort diagnostic; an unreachable
// backend is a different signal and is already surfaced via its
// BackendReport.InitErr.
func collectOrphans(cmd *cobra.Command) []orphanItemJSON {
	sysClient := cliutil.NewSystemClient()
	result, err := sysClient.Prune(cmd.Context(), yoloai.PruneOptions{DryRun: true})
	if err != nil || result == nil {
		return nil
	}
	out := make([]orphanItemJSON, 0, len(result.RemovedItems))
	for _, item := range result.RemovedItems {
		out = append(out, orphanItemJSON{
			Backend: string(item.Backend),
			Kind:    string(item.Kind),
			Name:    item.Name,
		})
	}
	return out
}

// renderOrphans prints the orphan-state section after the backend
// reports. Silent when nothing is leaking; loud (with a fix command)
// when something is.
func renderOrphans(w io.Writer, orphans []orphanItemJSON) {
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintln(w)                                                                                            //nolint:errcheck
	fmt.Fprintln(w, "Orphan state:")                                                                           //nolint:errcheck
	fmt.Fprintf(w, "  %d resource(s) left over from previous runs (no matching sandbox dir).\n", len(orphans)) //nolint:errcheck
	// Cap the inline list at 10 entries; the rest collapse to a
	// count. Doctor output is already verbose — we don't need to
	// emit dozens of names. `yoloai system prune --dry-run` shows
	// the full list for users who want it.
	const previewMax = 10
	preview := orphans
	if len(preview) > previewMax {
		preview = preview[:previewMax]
	}
	for _, o := range preview {
		fmt.Fprintf(w, "    %s/%s: %s\n", o.Backend, o.Kind, o.Name) //nolint:errcheck
	}
	if len(orphans) > previewMax {
		fmt.Fprintf(w, "    ... and %d more (run 'yoloai system prune --dry-run' for the full list)\n", len(orphans)-previewMax) //nolint:errcheck
	}
	fmt.Fprintln(w, "  Clean up with: yoloai system prune") //nolint:errcheck
}

// collectDoctorReports iterates over known backends and builds the full report list.
func collectDoctorReports(ctx context.Context, env caps.Environment, backendFilter, isolationFilter string) []caps.BackendReport {
	var reports []caps.BackendReport

	for _, desc := range runtime.Descriptors() {
		if backendFilter != "" && string(desc.Name) != backendFilter {
			continue
		}

		rt, err := cliutil.NewRuntime(ctx, desc.Name)
		if err != nil {
			if isolationFilter == "" {
				reports = append(reports, caps.BackendReport{
					Backend:      string(desc.Name),
					Mode:         "?",
					IsBaseMode:   true,
					InitErr:      err,
					Availability: caps.Unavailable,
				})
			}
			continue
		}

		if isolationFilter == "" {
			reports = append(reports, caps.BackendReport{
				Backend:      string(desc.Name),
				Mode:         string(rt.Descriptor().BaseModeName),
				IsBaseMode:   true,
				Availability: caps.Ready,
			})
		}

		for _, mode := range rt.Descriptor().SupportedIsolationModes {
			if isolationFilter != "" && string(mode) != isolationFilter {
				continue
			}
			capList := runtime.RequiredCapabilitiesFor(rt, mode)
			results := caps.RunChecks(ctx, capList, env)
			avail := caps.ComputeAvailability(results)
			reports = append(reports, caps.BackendReport{
				Backend:      string(desc.Name),
				Mode:         string(mode),
				IsBaseMode:   false,
				Results:      results,
				Availability: avail,
			})
		}

		_ = rt.Close() //nolint:errcheck,gosec // best-effort cleanup
	}

	return reports
}

// convertDoctorReportsToJSON converts BackendReport slice to JSON-serializable form.
func convertDoctorReportsToJSON(reports []caps.BackendReport) []backendReportJSON {
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
	return jsonReports
}
