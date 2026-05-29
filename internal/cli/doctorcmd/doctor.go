package doctorcmd

// ABOUTME: top-level `yoloai doctor` command — shows backend/isolation capability
// ABOUTME: status plus a read-only repair advisory (reclaimable cruft, reclaimable
// ABOUTME: cache space, broken sandboxes holding unreviewed work, and trash).

import (
	"context"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/runtime/caps"

	"github.com/spf13/cobra"
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

// reclaimItemJSON is one item Prune would remove right now (an orphaned backend
// resource, an orphaned lock file, a stale temp dir, or a never-init sandbox dir).
type reclaimItemJSON struct {
	Backend string `json:"backend,omitempty"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
}

// cacheUsageJSON is one backend's reclaimable cache footprint (image cache,
// snapshots, build cache). Reclaiming forces a base-image rebuild.
type cacheUsageJSON struct {
	Backend string `json:"backend"`
	Bytes   int64  `json:"bytes"`
	Detail  string `json:"detail,omitempty"`
}

// unreviewedWorkJSON is one broken sandbox dir that holds detectable user work.
// Prune refuses to touch it; the user must review and remove it explicitly.
type unreviewedWorkJSON struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

// trashJSON summarises the trash dir.
type trashJSON struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// doctorReportJSON is the single --json document: backend capability reports
// plus the read-only repair advisory sections.
type doctorReportJSON struct {
	Backends         []backendReportJSON  `json:"backends"`
	ReclaimableNow   []reclaimItemJSON    `json:"reclaimable_now"`
	ReclaimableSpace []cacheUsageJSON     `json:"reclaimable_space"`
	UnreviewedWork   []unreviewedWorkJSON `json:"unreviewed_work"`
	Trash            trashJSON            `json:"trash"`
}

// NewCmd builds the top-level `yoloai doctor` command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "doctor",
		Short:   "Diagnose backend capabilities and surface reclaimable state",
		GroupID: cliutil.GroupAdmin,
		Long: `Diagnose this machine's yoloai health.

Capability status per backend and isolation mode:
  Ready to use     — all prerequisites satisfied
  Needs setup      — prerequisites missing but fixable
  Not available    — hardware or OS mismatch (not actionable)

Plus a read-only repair advisory (nothing is deleted by doctor):
  Reclaimable now      — orphaned resources, lock files, temp dirs, never-init dirs
  Reclaimable space    — backend image/build caches (reclaim forces base rebuild)
  Unreviewed work      — broken sandbox dirs still holding work (review/remove yourself)
  Trash                — quarantined dirs recoverable with mv

Exit code 0 if no NeedsSetup entries; 1 if any NeedsSetup entries exist.
Unavailable entries and advisory sections do not affect the exit code.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backendFilter, _ := cmd.Flags().GetString("backend")
			isolationFilter, _ := cmd.Flags().GetString("isolation")
			return runDoctor(cmd, backendFilter, isolationFilter, cliutil.JSONEnabled(cmd))
		},
	}

	cmd.Flags().String("backend", "", "Check only this backend")
	cmd.Flags().String("isolation", "", "Check only this isolation mode")

	return cmd
}

func runDoctor(cmd *cobra.Command, backendFilter, isolationFilter string, isJSON bool) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	sys := cliutil.NewSystemClient()

	reports, err := sys.Doctor(ctx, yoloai.DoctorOptions{
		BackendFilter:   backendFilter,
		IsolationFilter: isolationFilter,
	})
	if err != nil {
		return err
	}

	// One dry-run prune + one disk-usage probe feed every advisory section.
	// doctor never mutates state — it delegates the actual cleanup to
	// `yoloai system prune` (with the fix command printed next to each section).
	prune := dryRunPrune(ctx, sys)
	disk := cacheUsage(ctx, sys)

	if isJSON {
		// JSON mode reports NeedsSetup in the document rather than via exit code,
		// matching the prior `system doctor --json` behavior.
		return cliutil.WriteJSON(out, buildDoctorJSON(reports, prune, disk))
	}

	caps.FormatDoctor(out, reports)
	renderReclaimableNow(out, prune)
	renderReclaimableSpace(out, disk)
	renderUnreviewedWork(out, prune)
	renderTrash(out, prune)

	return needsSetupError(reports)
}

// dryRunPrune runs a best-effort dry-run prune. Errors are swallowed — doctor
// is advisory; a failed probe just omits the section rather than aborting.
func dryRunPrune(ctx context.Context, sys *yoloai.SystemClient) *yoloai.PruneResult {
	result, err := sys.Prune(ctx, yoloai.PruneOptions{DryRun: true})
	if err != nil {
		return nil
	}
	return result
}

// cacheUsage runs a best-effort per-backend disk-usage probe. Errors are
// swallowed for the same reason as dryRunPrune.
func cacheUsage(ctx context.Context, sys *yoloai.SystemClient) *yoloai.DiskUsage {
	du, err := sys.DiskUsage(ctx)
	if err != nil {
		return nil
	}
	return du
}

// needsSetupError returns a non-nil error (→ exit 1) when any backend needs
// setup. Unavailable backends and advisory sections never trigger it.
func needsSetupError(reports []yoloai.BackendReport) error {
	for _, r := range reports {
		if r.Availability == caps.NeedsSetup {
			return fmt.Errorf("one or more backends need setup")
		}
	}
	return nil
}

const reclaimPreviewMax = 10

// renderReclaimableNow lists what `yoloai system prune` would remove right now.
func renderReclaimableNow(w io.Writer, prune *yoloai.PruneResult) {
	if prune == nil || len(prune.RemovedItems) == 0 {
		return
	}
	items := prune.RemovedItems
	fmt.Fprintln(w)                                                                                                          //nolint:errcheck
	fmt.Fprintln(w, "Reclaimable now:")                                                                                      //nolint:errcheck
	fmt.Fprintf(w, "  %d item(s): orphaned resources, lock files, temp dirs, never-initialized sandbox dirs.\n", len(items)) //nolint:errcheck
	preview := items
	if len(preview) > reclaimPreviewMax {
		preview = preview[:reclaimPreviewMax]
	}
	for _, it := range preview {
		renderReclaimItem(w, it)
	}
	if len(items) > reclaimPreviewMax {
		fmt.Fprintf(w, "    ... and %d more (run 'yoloai system prune --dry-run' for the full list)\n", len(items)-reclaimPreviewMax) //nolint:errcheck
	}
	fmt.Fprintln(w, "  Clean up with: yoloai system prune") //nolint:errcheck
}

func renderReclaimItem(w io.Writer, it yoloai.PruneItem) {
	if it.Backend != "" {
		fmt.Fprintf(w, "    %s/%s: %s\n", it.Backend, it.Kind, it.Name) //nolint:errcheck
		return
	}
	fmt.Fprintf(w, "    %s: %s\n", it.Kind, it.Name) //nolint:errcheck
}

// renderReclaimableSpace lists per-backend reclaimable cache size. Only shown
// when at least one backend reports a non-zero, error-free footprint.
func renderReclaimableSpace(w io.Writer, disk *yoloai.DiskUsage) {
	if disk == nil {
		return
	}
	var total int64
	var rows []yoloai.BackendDiskUsage
	for _, b := range disk.PerBackend {
		if b.Err != nil || b.Bytes == 0 {
			continue
		}
		rows = append(rows, b)
		total += b.Bytes
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w)                                                                           //nolint:errcheck
	fmt.Fprintln(w, "Reclaimable space (backend caches — DESTRUCTIVE, forces base rebuild):") //nolint:errcheck
	for _, b := range rows {
		fmt.Fprintf(w, "    %s: %s\n", b.Name, cliutil.HumanBytes(b.Bytes)) //nolint:errcheck
	}
	fmt.Fprintf(w, "  Total: %s\n", cliutil.HumanBytes(total))     //nolint:errcheck
	fmt.Fprintln(w, "  Reclaim with: yoloai system prune --cache") //nolint:errcheck
}

// renderUnreviewedWork lists broken sandbox dirs that still hold detectable
// work — prune refuses these; the user reviews and removes them.
func renderUnreviewedWork(w io.Writer, prune *yoloai.PruneResult) {
	if prune == nil || len(prune.RefusedDataBearing) == 0 {
		return
	}
	fmt.Fprintln(w)                                              //nolint:errcheck
	fmt.Fprintln(w, "Broken sandboxes holding unreviewed work:") //nolint:errcheck
	for _, r := range prune.RefusedDataBearing {
		fmt.Fprintf(w, "  %s — %s\n", r.Name, r.Detail)                                             //nolint:errcheck
		fmt.Fprintf(w, "    review: yoloai diff %s    remove: yoloai destroy %s\n", r.Name, r.Name) //nolint:errcheck
	}
}

// renderTrash summarises the trash dir and how to recover or reclaim it.
func renderTrash(w io.Writer, prune *yoloai.PruneResult) {
	if prune == nil || prune.TrashContents.Count == 0 {
		return
	}
	t := prune.TrashContents
	fmt.Fprintln(w)                                                                        //nolint:errcheck
	fmt.Fprintf(w, "Trash holds %d item(s) (%s).\n", t.Count, cliutil.HumanBytes(t.Bytes)) //nolint:errcheck
	fmt.Fprintln(w, "  Recover with mv, or reclaim with: yoloai system prune")             //nolint:errcheck
}

// buildDoctorJSON assembles the single --json document.
func buildDoctorJSON(reports []yoloai.BackendReport, prune *yoloai.PruneResult, disk *yoloai.DiskUsage) doctorReportJSON {
	return doctorReportJSON{
		Backends:         convertDoctorReportsToJSON(reports),
		ReclaimableNow:   reclaimItemsJSON(prune),
		ReclaimableSpace: cacheUsageJSONList(disk),
		UnreviewedWork:   unreviewedWorkJSONList(prune),
		Trash:            trashJSONOf(prune),
	}
}

func reclaimItemsJSON(prune *yoloai.PruneResult) []reclaimItemJSON {
	if prune == nil {
		return []reclaimItemJSON{}
	}
	out := make([]reclaimItemJSON, 0, len(prune.RemovedItems))
	for _, it := range prune.RemovedItems {
		out = append(out, reclaimItemJSON{
			Backend: string(it.Backend),
			Kind:    string(it.Kind),
			Name:    it.Name,
		})
	}
	return out
}

func cacheUsageJSONList(disk *yoloai.DiskUsage) []cacheUsageJSON {
	if disk == nil {
		return []cacheUsageJSON{}
	}
	out := make([]cacheUsageJSON, 0, len(disk.PerBackend))
	for _, b := range disk.PerBackend {
		if b.Err != nil || b.Bytes == 0 {
			continue
		}
		out = append(out, cacheUsageJSON{
			Backend: string(b.Name),
			Bytes:   b.Bytes,
			Detail:  b.Detail,
		})
	}
	return out
}

func unreviewedWorkJSONList(prune *yoloai.PruneResult) []unreviewedWorkJSON {
	if prune == nil {
		return []unreviewedWorkJSON{}
	}
	out := make([]unreviewedWorkJSON, 0, len(prune.RefusedDataBearing))
	for _, r := range prune.RefusedDataBearing {
		out = append(out, unreviewedWorkJSON{Name: r.Name, Path: r.Path, Detail: r.Detail})
	}
	return out
}

func trashJSONOf(prune *yoloai.PruneResult) trashJSON {
	if prune == nil {
		return trashJSON{}
	}
	return trashJSON{Count: prune.TrashContents.Count, Bytes: prune.TrashContents.Bytes}
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
