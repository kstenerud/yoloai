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

	"github.com/spf13/cobra"
)

// checkResultJSON is the JSON-serializable form of a single capability check result.
type checkResultJSON struct {
	CapID       string           `json:"cap_id"`
	CapSummary  string           `json:"cap_summary"`
	OK          bool             `json:"ok"`
	IsPermanent bool             `json:"is_permanent,omitempty"`
	Error       string           `json:"error,omitempty"`
	Steps       []yoloai.FixStep `json:"steps,omitempty"`
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

// cacheUsageJSON is one backend's reclaimable footprint, split by whether
// reclaiming forces a rebuild. CachedBytes (build cache, volumes, dangling
// images) is freed by `yoloai system prune` with no rebuild; ImageBytes (base
// images) is freed only by `yoloai system prune --images`, which forces a
// rebuild. ImageBytes is omitted when the backend can't size it cheaply.
type cacheUsageJSON struct {
	Backend     string `json:"backend"`
	CachedBytes int64  `json:"cached_bytes"`
	ImageBytes  int64  `json:"image_bytes,omitempty"`
	StaleBytes  int64  `json:"stale_bytes,omitempty"`
	Detail      string `json:"detail,omitempty"`
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

// vmSlotJSON is one VM occupying a host VM slot.
type vmSlotJSON struct {
	PID     int    `json:"pid"`
	VMName  string `json:"vm_name,omitempty"`
	Owned   bool   `json:"owned"`
	Deleted bool   `json:"deleted,omitempty"`
}

// vmCensusJSON is the host VM-slot census for a concurrency-limited backend.
type vmCensusJSON struct {
	Limit   int          `json:"limit"`
	InUse   int          `json:"in_use"`
	Blocked bool         `json:"blocked"`
	Slots   []vmSlotJSON `json:"slots"`
}

// vmNetHealthJSON is one running VM's network-liveness probe result.
type vmNetHealthJSON struct {
	SandboxName string `json:"sandbox_name"`
	VMName      string `json:"vm_name"`
	State       string `json:"state"`
	Detail      string `json:"detail,omitempty"`
}

// netLivenessJSON is the guest-network liveness probe across running VMs.
type netLivenessJSON struct {
	VMs []vmNetHealthJSON `json:"vms"`
}

// doctorReportJSON is the single --json document: backend capability reports
// plus the read-only repair advisory sections.
type doctorReportJSON struct {
	Backends         []backendReportJSON  `json:"backends"`
	ReclaimableNow   []reclaimItemJSON    `json:"reclaimable_now"`
	ReclaimableSpace []cacheUsageJSON     `json:"reclaimable_space"`
	UnreviewedWork   []unreviewedWorkJSON `json:"unreviewed_work"`
	Trash            trashJSON            `json:"trash"`
	VMCensus         *vmCensusJSON        `json:"vm_census,omitempty"`
	NetLiveness      *netLivenessJSON     `json:"net_liveness,omitempty"`
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
  Reclaimable now        — orphaned resources, lock files, temp dirs, never-init dirs
  Reclaimable cached data — build caches reclaimable by 'prune' (no rebuild)
  Reclaimable images     — base images reclaimable only by 'prune --images' (forces rebuild)
  Superseded base images — old-macOS bases reclaimable by 'prune --stale-bases' (no rebuild)
  Unreviewed work        — broken sandbox dirs still holding work (review/remove yourself)
  Trash                  — quarantined dirs recoverable with mv

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
	sys, err := cliutil.System()
	if err != nil {
		return err
	}

	reports, err := sys.Doctor(ctx, yoloai.SystemDoctorOptions{
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
	census := sys.VMCensus(ctx)
	netLiveness := sys.NetLiveness(ctx)

	if isJSON {
		// JSON mode reports failures in the document rather than via exit code,
		// matching the prior `system doctor --json` behavior.
		return cliutil.WriteJSON(out, buildDoctorJSON(reports, prune, disk, census, netLiveness))
	}

	formatDoctor(out, reports)
	renderVMCensus(out, census)
	renderNetLiveness(out, netLiveness)
	renderReclaimableNow(out, prune)
	renderReclaimableSpace(out, disk)
	renderUnreviewedWork(out, prune)
	renderTrash(out, prune)

	return doctorExitError(reports, census, netLiveness)
}

// doctorExitError returns a non-nil error (→ exit 1) when the host needs
// attention: a backend needs setup, the VM-slot limit is reached (which
// blocks new sandboxes), or a VM's guest network is confirmed wedged (which
// silently breaks the agent inside it) — all functional failures, unlike the
// advisory cruft sections, which never affect the exit code.
func doctorExitError(reports []yoloai.BackendReport, census *yoloai.VMCensus, netLiveness *yoloai.NetLivenessReport) error {
	if err := needsSetupError(reports); err != nil {
		return err
	}
	if census != nil && census.Blocked() {
		return fmt.Errorf("macOS VM limit reached — see 'VM slots' above")
	}
	if netLiveness != nil && len(netLiveness.Wedged()) > 0 {
		return fmt.Errorf("a VM's guest network is wedged — see 'network' above")
	}
	return nil
}

// dryRunPrune runs a best-effort dry-run prune. Errors are swallowed — doctor
// is advisory; a failed probe just omits the section rather than aborting.
func dryRunPrune(ctx context.Context, sys *yoloai.System) *yoloai.PruneResult {
	result, err := sys.Prune(ctx, yoloai.SystemPruneOptions{DryRun: true})
	if err != nil {
		return nil
	}
	return result
}

// cacheUsage runs a best-effort per-backend disk-usage probe. Errors are
// swallowed for the same reason as dryRunPrune.
func cacheUsage(ctx context.Context, sys *yoloai.System) *yoloai.DiskUsage {
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
		if r.Availability == yoloai.NeedsSetup {
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
	if it.BackendType != "" {
		fmt.Fprintf(w, "    %s/%s: %s\n", it.BackendType, it.Kind, it.Name) //nolint:errcheck
		return
	}
	fmt.Fprintf(w, "    %s: %s\n", it.Kind, it.Name) //nolint:errcheck
}

// renderReclaimableSpace lists per-backend reclaimable bytes in two tiers:
// cached data that plain `prune` frees without a rebuild, and base images that
// only `prune --images` frees (forcing a rebuild). Each tier is shown only when
// at least one backend reports a non-zero, error-free footprint for it.
func renderReclaimableSpace(w io.Writer, disk *yoloai.DiskUsage) {
	if disk == nil {
		return
	}
	renderReclaimTier(w, disk, "Reclaimable cached data that's no longer needed:",
		"build cache + volumes", "yoloai system prune",
		func(b yoloai.BackendDiskUsage) int64 { return b.CachedBytes })
	renderReclaimTier(w, disk, "Reclaimable images (these will need to be regenerated to use yoloAI):",
		"base images", "yoloai system prune --images",
		func(b yoloai.BackendDiskUsage) int64 { return b.ImageBytes })
	renderReclaimTier(w, disk, "Superseded base images from a previous macOS (safe to remove, no rebuild):",
		"superseded base", "yoloai system prune --stale-bases",
		func(b yoloai.BackendDiskUsage) int64 { return b.StaleBytes })
}

// renderReclaimTier prints one reclaim section (cached-data or images). bytesOf
// selects the relevant field; values <= 0 (nothing, or the unknown sentinel)
// are skipped so they don't render as "-1 B" or poison the total.
func renderReclaimTier(w io.Writer, disk *yoloai.DiskUsage, header, label, command string, bytesOf func(yoloai.BackendDiskUsage) int64) {
	var total int64
	var rows []yoloai.BackendDiskUsage
	for _, b := range disk.PerBackend {
		if b.Err != nil || bytesOf(b) <= 0 {
			continue
		}
		rows = append(rows, b)
		total += bytesOf(b)
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w)         //nolint:errcheck
	fmt.Fprintln(w, header) //nolint:errcheck
	for _, b := range rows {
		fmt.Fprintf(w, "    %s: %s %s\n", b.Type, cliutil.HumanBytes(bytesOf(b)), label) //nolint:errcheck
	}
	fmt.Fprintf(w, "  Reclaim with: %s\n", command) //nolint:errcheck
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

// renderVMCensus shows the host VM-slot census when it's notable: the limit is
// reached (blocking new sandboxes) or a leaked orphan is holding a slot. It
// enumerates every slot so the user can see exactly what's consuming the limit,
// then prints how to free a slot. Nothing is killed — the kill command is only
// printed.
func renderVMCensus(w io.Writer, census *yoloai.VMCensus) {
	if census == nil {
		return
	}
	orphans := census.Orphans()
	if !census.Blocked() && len(orphans) == 0 {
		return // below limit and nothing leaked — not worth a line
	}

	fmt.Fprintln(w) //nolint:errcheck
	if census.Blocked() {
		fmt.Fprintf(w, "VM slots: %d of %d in use — limit reached, new sandboxes can't start:\n", census.InUse(), census.Limit) //nolint:errcheck
	} else {
		fmt.Fprintf(w, "VM slots: %d of %d in use (%d orphaned):\n", census.InUse(), census.Limit, len(orphans)) //nolint:errcheck
	}
	for _, s := range census.Slots {
		fmt.Fprintf(w, "    %s\n", vmSlotLabel(s)) //nolint:errcheck
	}

	switch {
	case len(orphans) > 0:
		fmt.Fprintln(w, "  Free a slot by killing the orphaned VM process(es):") //nolint:errcheck
		for _, s := range orphans {
			fmt.Fprintf(w, "    kill %d\n", s.PID) //nolint:errcheck
		}
		fmt.Fprintln(w, "  (orphans only clear on kill or reboot — they survive a crashed launcher)") //nolint:errcheck
	case census.Blocked():
		// All slots are legitimate running sandboxes.
		fmt.Fprintln(w, "  All slots are in-use sandboxes. Stop one to free a slot:") //nolint:errcheck
		fmt.Fprintln(w, "    yoloai stop <name>")                                     //nolint:errcheck
	}
}

// vmSlotLabel renders a one-line description of a VM slot.
func vmSlotLabel(s yoloai.VMSlot) string {
	if s.Owned {
		if s.VMName != "" {
			return fmt.Sprintf("running sandbox '%s' (pid %d) — owned", s.VMName, s.PID)
		}
		return fmt.Sprintf("running VM (pid %d) — owned", s.PID)
	}
	switch {
	case s.VMName != "" && s.Deleted:
		return fmt.Sprintf("orphaned VM '%s' (pid %d, image deleted) — launcher gone, holding a slot", s.VMName, s.PID)
	case s.VMName != "":
		return fmt.Sprintf("orphaned VM '%s' (pid %d) — launcher gone, holding a slot", s.VMName, s.PID)
	default:
		return fmt.Sprintf("orphaned VM (pid %d) — launcher gone, holding a slot", s.PID)
	}
}

// renderNetLiveness shows one line per running VM's guest-network liveness.
// Nothing is printed when there's no report (backend doesn't support the
// probe, or platform has no running VMs to check) — mirrors renderVMCensus's
// silence-on-absence.
func renderNetLiveness(w io.Writer, report *yoloai.NetLivenessReport) {
	if report == nil || len(report.VMs) == 0 {
		return
	}
	fmt.Fprintln(w) //nolint:errcheck
	for _, vm := range report.VMs {
		fmt.Fprintf(w, "%s: %s\n", vm.SandboxName, netHealthLabel(vm)) //nolint:errcheck
	}
}

// netHealthLabel renders one VM's network-liveness line. The wedged message
// is directive: report only, never restart automatically — the user runs the
// stop/start themselves, and agent session state on disk survives it.
func netHealthLabel(vm yoloai.VMNetHealth) string {
	switch vm.State {
	case yoloai.NetHealthOK:
		return fmt.Sprintf("network: ok (%s)", vm.Detail)
	case yoloai.NetHealthWedged:
		return fmt.Sprintf(
			"network: WEDGED (%s) — vmnet session is dead; "+
				"only a restart recovers it: yoloai stop %s && yoloai start %s "+
				"(agent session state on disk survives). A wedged VM also breaks "+
				"networking for NEW tart VMs on this host.",
			vm.Detail, vm.SandboxName, vm.SandboxName)
	default:
		return fmt.Sprintf(
			"network: could not determine (%s) — if the VM booted moments ago, "+
				"DHCP may still be pending; re-run doctor", vm.Detail)
	}
}

// buildDoctorJSON assembles the single --json document.
func buildDoctorJSON(reports []yoloai.BackendReport, prune *yoloai.PruneResult, disk *yoloai.DiskUsage, census *yoloai.VMCensus, netLiveness *yoloai.NetLivenessReport) doctorReportJSON {
	return doctorReportJSON{
		Backends:         convertDoctorReportsToJSON(reports),
		ReclaimableNow:   reclaimItemsJSON(prune),
		ReclaimableSpace: cacheUsageJSONList(disk),
		UnreviewedWork:   unreviewedWorkJSONList(prune),
		Trash:            trashJSONOf(prune),
		VMCensus:         vmCensusJSONOf(census),
		NetLiveness:      netLivenessJSONOf(netLiveness),
	}
}

func vmCensusJSONOf(census *yoloai.VMCensus) *vmCensusJSON {
	if census == nil {
		return nil
	}
	slots := make([]vmSlotJSON, 0, len(census.Slots))
	for _, s := range census.Slots {
		slots = append(slots, vmSlotJSON{PID: s.PID, VMName: s.VMName, Owned: s.Owned, Deleted: s.Deleted})
	}
	return &vmCensusJSON{
		Limit:   census.Limit,
		InUse:   census.InUse(),
		Blocked: census.Blocked(),
		Slots:   slots,
	}
}

// netHealthStateJSON renders a NetHealthState as a lowercase JSON string.
func netHealthStateJSON(s yoloai.NetHealthState) string {
	switch s {
	case yoloai.NetHealthOK:
		return "ok"
	case yoloai.NetHealthWedged:
		return "wedged"
	default:
		return "unknown"
	}
}

func netLivenessJSONOf(report *yoloai.NetLivenessReport) *netLivenessJSON {
	if report == nil {
		return nil
	}
	vms := make([]vmNetHealthJSON, 0, len(report.VMs))
	for _, vm := range report.VMs {
		vms = append(vms, vmNetHealthJSON{
			SandboxName: vm.SandboxName,
			VMName:      vm.VMName,
			State:       netHealthStateJSON(vm.State),
			Detail:      vm.Detail,
		})
	}
	return &netLivenessJSON{VMs: vms}
}

func reclaimItemsJSON(prune *yoloai.PruneResult) []reclaimItemJSON {
	if prune == nil {
		return []reclaimItemJSON{}
	}
	out := make([]reclaimItemJSON, 0, len(prune.RemovedItems))
	for _, it := range prune.RemovedItems {
		out = append(out, reclaimItemJSON{
			Backend: string(it.BackendType),
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
		// Match renderReclaimableSpace: omit errored backends, and omit any
		// backend with nothing reclaimable in either tier. A negative ImageBytes
		// is the unknown sentinel — clamp it to 0 so a JSON consumer never sees a
		// nonsensical negative count (it's then omitempty-elided).
		if b.Err != nil {
			continue
		}
		imageBytes := b.ImageBytes
		if imageBytes < 0 {
			imageBytes = 0
		}
		if b.CachedBytes <= 0 && imageBytes <= 0 && b.StaleBytes <= 0 {
			continue
		}
		out = append(out, cacheUsageJSON{
			Backend:     string(b.Type),
			CachedBytes: b.CachedBytes,
			ImageBytes:  imageBytes,
			StaleBytes:  b.StaleBytes,
			Detail:      b.Detail,
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
func convertDoctorReportsToJSON(reports []yoloai.BackendReport) []backendReportJSON {
	jsonReports := make([]backendReportJSON, 0, len(reports))
	for _, r := range reports {
		jr := backendReportJSON{
			Backend:    string(r.Type),
			Mode:       r.Mode,
			IsBaseMode: r.IsBaseMode,
		}
		switch r.Availability {
		case yoloai.Ready:
			jr.Availability = "ready"
		case yoloai.NeedsSetup:
			jr.Availability = "needs_setup"
		default:
			jr.Availability = "unavailable"
		}
		if r.InitErr != nil {
			jr.InitError = r.InitErr.Error()
		}
		for _, cr := range r.Results {
			jcr := checkResultJSON{
				CapID:      cr.Capability.ID,
				CapSummary: cr.Capability.Summary,
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
