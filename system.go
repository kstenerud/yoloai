// ABOUTME: System — admin sub-handle off Client. Hosts cross-backend
// ABOUTME: operations (disk usage, prune, image build, check) that are scoped to the
// ABOUTME: host rather than to a specific sandbox.
package yoloai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// System scopes `yoloai system …` operations. It is a sub-handle off a
// Client, obtained via Client.System() — always non-nil, never errors, and
// pure namespace expansion off the Client's layout (no IO at construction).
//
// Decoupled from a specific backend on purpose: cross-backend
// methods (DiskUsage, Prune, BuildImage with BackendsAll) iterate every
// registered backend that's available in the current environment
// and spin up an ephemeral runtime per backend. Single-backend
// methods (CheckPrerequisites, single-backend BuildImage) take a BackendType parameter.
//
// Safe for concurrent use by multiple goroutines. Read-only methods
// (DiskUsage, CheckPrerequisites) run in parallel. Write methods (BuildImage, Prune)
// acquire backend-internal locks where applicable.
type System struct {
	layout config.Layout
}

// Config returns the configuration-management sub-handle.
//
// Q-W resolution (Shape B, sub-handles): config get/set/reset
// cluster under one accessor, matching Profiles().
func (s *System) Config() *ConfigAdmin {
	return &ConfigAdmin{s: s}
}

// Profiles returns the profile-management sub-handle.
//
// Q-W resolution (Shape B, sub-handles): profile admin is grouped
// behind one accessor so the System root stays uncluttered as
// admin verbs grow. Mirrors the same pattern Config() uses.
func (s *System) Profiles() *ProfileAdmin {
	return &ProfileAdmin{s: s}
}

// TartBases returns the admin handle for Tart simulator runtime base images.
// The Tart backend is macOS-only; runtime-touching methods (List/Add/Remove)
// return the backend-construction error when it is unavailable. Call Available
// to probe first, or inspect the returned error.
func (s *System) TartBases() *TartBaseAdmin {
	return &TartBaseAdmin{layout: s.layout}
}

// LayoutStatus is the verdict of a realm status check — see DataDirStatus.
// Re-exported (type alias) from internal/config so embedders never name it.
type LayoutStatus = config.LayoutStatus

// Re-export the LayoutStatus values so embedders can switch on DataDirStatus
// without importing internal/config.
const (
	// LayoutFresh: the DataDir is absent or empty — create it fresh.
	LayoutFresh = config.LayoutFresh
	// LayoutMigrate: the DataDir exists at an older version — migrate it.
	LayoutMigrate = config.LayoutMigrate
	// LayoutOK: the DataDir is at the current version — ready to use.
	LayoutOK = config.LayoutOK
)

// DataDirStatus reports what the library realm's DataDir needs before use:
// LayoutFresh (create), LayoutMigrate (run MigrateDataDir), or LayoutOK (proceed). A
// too-new on-disk version returns an error (the binary is older than the data;
// the user must upgrade yoloai). It is a cheap, read-only check that inspects
// only the DataDir and its plain-int .schema-version stamp.
//
// Direct embedders that own a dedicated DataDir use this to decide between
// CreateDataDir and MigrateDataDir; the CLI's startup gate calls it as one of its two
// realm checks.
func (s *System) DataDirStatus() (LayoutStatus, error) {
	return config.RealmStatus(s.layout.DataDir, config.LibrarySchemaVersion)
}

// CreateDataDir initializes the library realm's DataDir at the current schema
// version (directory + version stamp). Call it only when DataDirStatus reports
// LayoutFresh; operational scaffolding is still materialized lazily by the
// engine's setup path.
func (s *System) CreateDataDir() error {
	return config.CreateFreshLibrary(s.layout)
}

// MigrateDataDir brings the library realm's DataDir up to the current schema version.
// Idempotent: a DataDir already at the current version is a no-op. This is the
// only entry point that mutates on-disk schema state for the library realm;
// the engine no longer migrates as a side effect of setup.
func (s *System) MigrateDataDir(ctx context.Context) error {
	_ = ctx
	return config.MigrateLibrary(s.layout)
}

// DiskUsage reports total on-disk usage by yoloai and each available
// backend. Walks the sandboxes directory for yoloai's own footprint
// and queries each backend's CacheUsage. Backends that fail to report
// surface their error in the per-backend entry rather than aborting
// the whole call.
type DiskUsage struct {
	// SandboxesBytes is the total size under DataDir/sandboxes/.
	SandboxesBytes int64
	// PerBackend has one entry per backend that was probed available.
	// Order matches runtime.Descriptors() (registration order).
	PerBackend []BackendDiskUsage
}

// BackendDiskUsage is one row of DiskUsage's per-backend section, split by
// whether reclaiming the space forces a base-image rebuild. When Err is
// non-nil, the byte counts are 0 and Detail carries any partial progress info
// from the backend.
type BackendDiskUsage struct {
	Type BackendType
	// CachedBytes is reclaimable by plain `prune` without a rebuild (build
	// cache, volumes). Always >= 0.
	CachedBytes int64
	// ImageBytes is reclaimable only by `prune --images`, forcing a rebuild
	// (base/profile image layers). -1 when the backend can't report a size.
	ImageBytes int64
	Detail     string
	Err        error
}

// DiskUsage returns a per-backend disk-usage snapshot plus yoloai's
// own sandboxes-directory size. Unavailable backends are skipped.
func (s *System) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	du := &DiskUsage{
		SandboxesBytes: dirSize(s.layout.SandboxesDir()),
	}
	for _, desc := range runtime.Descriptors() {
		rt, err := newRuntime(ctx, desc.Type, s.layout)
		if err != nil {
			// Backend not available in this environment — skip silently.
			// The CLI's `yoloai system disk` does the same filtering via
			// checkBackend before calling per-backend code.
			continue
		}
		usage, usageErr := runtime.CacheUsageFor(ctx, rt)
		_ = rt.Close()
		du.PerBackend = append(du.PerBackend, BackendDiskUsage{
			Type:        desc.Type,
			CachedBytes: usage.CachedBytes,
			ImageBytes:  usage.ImageBytes,
			Detail:      usage.Detail,
			Err:         usageErr,
		})
	}
	return du, nil
}

// SystemInfo describes a yoloai installation: where its state lives and which
// backends are usable. Build metadata (version/commit/date) is the CLI's
// concern and is intentionally not included. Disk usage is a separate (slower)
// call — see DiskUsage.
type SystemInfo struct {
	DataDir            string
	SandboxesDir       string
	GlobalConfigPath   string
	DefaultsConfigPath string
	Backends           []BackendInfo
}

// AllSandboxes enumerates sandboxes across every backend that currently
// has sandbox state, inspecting each via its own backend. Returns the sandbox
// infos plus the names of backends that have sandbox dirs but couldn't be
// reached (e.g. their daemon is down) so callers can warn without failing.
func (s *System) AllSandboxes(ctx context.Context) ([]*SandboxInfo, []BackendType, error) {
	infos, unavailable, err := sandbox.ListSandboxesMultiBackend(ctx, s.layout,
		func(ctx context.Context, backend runtime.BackendType) (runtime.Runtime, error) {
			return newRuntime(ctx, backend, s.layout)
		})
	if err != nil {
		return nil, nil, err
	}
	unavailableNames := make([]BackendType, len(unavailable))
	for i, name := range unavailable {
		unavailableNames[i] = BackendType(name)
	}
	return sandboxInfosFromStatus(infos), unavailableNames, nil
}

// ValidateSandboxName reports whether name is a well-formed sandbox name
// (allowed charset, no path-traversal). It consults no host state, so a daemon
// or CLI can pre-validate a name before any other verb is called.
func (s *System) ValidateSandboxName(name string) error {
	return store.ValidateName(name)
}

// Info returns the installation's paths and per-backend availability in one
// call. It never returns an error today (per-backend probe failures are
// captured in BackendInfo.Note); the error return is kept for forward
// compatibility, mirroring DiskUsage.
func (s *System) Info(ctx context.Context) (*SystemInfo, error) {
	return &SystemInfo{
		DataDir:            s.layout.YoloaiDir(),
		SandboxesDir:       s.layout.SandboxesDir(),
		GlobalConfigPath:   s.layout.GlobalConfigPath(),
		DefaultsConfigPath: s.layout.DefaultsConfigPath(),
		Backends:           s.BackendTypes(ctx, BackendQuery{ProbeAvailability: true}),
	}, nil
}

// VMCensus is a point-in-time accounting of host VM slots against the
// platform's concurrent-VM limit. Re-exported (type alias) from
// internal/runtime.
type VMCensus = runtime.VMCensus

// VMSlot describes one VM occupying a host VM slot. Re-exported (type alias)
// from internal/runtime.
type VMSlot = runtime.VMSlot

// SystemDoctorOptions filters Doctor's per-backend health checks. Empty filters
// (the zero value) report every backend and every isolation mode.
type SystemDoctorOptions struct {
	// BackendFilter limits the report to a single backend by name ("" = all).
	BackendFilter string
	// IsolationFilter limits the report to a single isolation mode ("" = all,
	// and the base-mode availability rows are included). When set, only the
	// matching per-isolation-mode rows are reported.
	IsolationFilter string
}

// Doctor probes each registered backend's health: base-mode availability and,
// per supported isolation mode, the host capabilities required and whether they
// are satisfied. It detects the host environment once and constructs an
// ephemeral runtime per backend (unavailable backends are reported, not fatal).
func (s *System) Doctor(ctx context.Context, opts SystemDoctorOptions) ([]BackendReport, error) {
	env := caps.DetectEnvironment()
	reports := make([]BackendReport, 0)
	for _, desc := range runtime.Descriptors() {
		if opts.BackendFilter != "" && string(desc.Type) != opts.BackendFilter {
			continue
		}
		for _, r := range s.backendReports(ctx, desc.Type, env, opts.IsolationFilter) {
			reports = append(reports, backendReportFromCaps(r))
		}
	}
	return reports, nil
}

// VMCensus reports the host VM-slot census for whichever backend runs under a
// concurrent-VM limit (currently only tart on macOS). Returns nil when no such
// backend is available — e.g. on Linux, or when tart can't be constructed.
// Best-effort: a backend that errors while reporting is skipped.
func (s *System) VMCensus(ctx context.Context) *VMCensus {
	for _, desc := range runtime.Descriptors() {
		rt, err := newRuntime(ctx, desc.Type, s.layout)
		if err != nil {
			continue
		}
		census, ok, censusErr := runtime.VMCensusFor(ctx, rt)
		_ = rt.Close() //nolint:errcheck // best-effort close after probing
		if !ok || censusErr != nil {
			continue
		}
		return &census
	}
	return nil
}

// backendReports builds the report rows for a single backend: an init-failure
// row if it can't be constructed, otherwise a base-mode row (unless filtered)
// plus one row per matching supported isolation mode.
func (s *System) backendReports(ctx context.Context, backend BackendType, env caps.Environment, isolationFilter string) []caps.BackendReport {
	rt, err := newRuntime(ctx, backend, s.layout)
	if err != nil {
		if isolationFilter != "" {
			return nil // an unavailable backend has no isolation-mode rows to filter to
		}
		return []caps.BackendReport{{
			Backend: string(backend), Mode: "?", IsBaseMode: true,
			InitErr: err, Availability: caps.Unavailable,
		}}
	}
	defer rt.Close() //nolint:errcheck // best-effort close after probing

	var reports []caps.BackendReport
	if isolationFilter == "" {
		reports = append(reports, caps.BackendReport{
			Backend: string(backend), Mode: string(rt.Descriptor().BaseModeName),
			IsBaseMode: true, Availability: caps.Ready,
		})
	}
	for _, mode := range rt.Descriptor().SupportedIsolationModes {
		if isolationFilter != "" && string(mode) != isolationFilter {
			continue
		}
		results := caps.RunChecks(ctx, runtime.RequiredCapabilitiesFor(rt, mode), env)
		reports = append(reports, caps.BackendReport{
			Backend: string(backend), Mode: string(mode), IsBaseMode: false,
			Results: results, Availability: caps.ComputeAvailability(results),
		})
	}
	return reports
}

// BuildImageOptions configures System.BuildImage.
type BuildImageOptions struct {
	// Profile is the profile name to build. Empty = base image only.
	// "base" is reserved and rejected (use Profile="" for the base image).
	Profile string
	// BackendType selects which backend(s) to build for. Required — pass a
	// specific backend (BackendDocker, …), or a reserved selector: BackendsAll
	// (every registered backend) or BackendDefault (the config-resolved
	// container backend). Empty is rejected; there is no implicit default.
	BackendType BackendType
	// Rebuild forces a build even when the checksum says the existing
	// image is current.
	Rebuild bool
	// Secrets are pre-validated --secret entries
	// (`id=<name>,src=<path>` form) to pass through to the build.
	Secrets []string
	// Output receives the raw build stream (docker / buildx output).
	// nil = io.Discard.
	Output io.Writer
}

// BuildImage builds the base image (Profile == "") or a profile image
// (Profile != "") for one backend or all available backends. Returns
// the first error from any backend; later backends in the iteration
// are skipped.
func (s *System) BuildImage(ctx context.Context, opts BuildImageOptions) error {
	if opts.Profile != "" {
		if err := config.ValidateProfileName(opts.Profile); err != nil {
			return err
		}
		if !config.ProfileExists(s.layout, opts.Profile) {
			return yoerrors.NewUsageError("profile %q does not exist", opts.Profile)
		}
		if err := s.profileHasDockerfile(opts.Profile); err != nil {
			return err
		}
	} else if len(opts.Secrets) > 0 {
		return yoerrors.NewUsageError("Secrets is only supported with a non-empty Profile")
	}

	out := opts.Output
	if out == nil {
		out = io.Discard
	}

	switch opts.BackendType {
	case "":
		return yoerrors.NewUsageError("BuildImageOptions.BackendType is required; pass a backend, BackendDefault, or BackendsAll")
	case BackendsAll:
		return s.buildAllBackends(ctx, opts, out)
	case BackendDefault:
		// Build targets the container slot — no isolation/OS routing.
		return s.buildOne(ctx, resolveBackendFromConfig(ctx, s.layout), opts, out)
	default:
		return s.buildOne(ctx, opts.BackendType, opts, out)
	}
}

// buildAllBackends builds for every registered backend, stopping on the first
// failure (matches the CLI's existing behavior). A more permissive best-effort
// policy can be added if users want it.
func (s *System) buildAllBackends(ctx context.Context, opts BuildImageOptions, out io.Writer) error {
	var built int
	for _, desc := range runtime.Descriptors() {
		if err := s.buildOne(ctx, desc.Type, opts, out); err != nil {
			return fmt.Errorf("build %s: %w", desc.Type, err)
		}
		built++
	}
	if built == 0 {
		return fmt.Errorf("no available backends to build for")
	}
	return nil
}

// buildOne runs one backend's build (base or profile) using a freshly
// constructed runtime that's closed before return.
func (s *System) buildOne(ctx context.Context, backend BackendType, opts BuildImageOptions, out io.Writer) error {
	rt, err := newRuntime(ctx, backend, s.layout)
	if err != nil {
		return err
	}
	defer rt.Close() //nolint:errcheck // best-effort
	if opts.Profile != "" {
		return sandbox.EnsureProfileImage(ctx, rt, s.layout, opts.Profile, opts.Secrets, out, slog.Default(), opts.Rebuild)
	}
	return rt.Setup(ctx, s.layout, s.layout.ProfileDir("base"), out, slog.Default(), opts.Rebuild)
}

// CheckPrerequisitesOptions configures System.Check.
type CheckPrerequisitesOptions struct {
	// Backend is the backend to verify. Required.
	BackendType BackendType
	// AgentType is the agent name whose credentials are checked. Required;
	// caller resolves the default before calling.
	AgentType AgentType
	// Isolation, when non-empty, triggers an isolation-mode capability
	// check via runtime.RequiredCapabilitiesFor + caps.RunChecks.
	Isolation IsolationMode
}

// CheckResult is one row of Check's output.
type CheckResult struct {
	// Name is the check identifier: "backend", "image", "agent", "isolation".
	Name string
	// OK is true when the check passed.
	OK bool
	// Message is human-readable extra detail. Empty for trivially-ok results.
	Message string
}

// CheckPrerequisites verifies that yoloai's prerequisites are satisfied. Runs all
// configured checks (always returns a result per check) and returns
// the result list. The error return is non-nil only for system-level
// failures (e.g. unknown backend); per-check failures are reflected
// in CheckResult.OK = false.
//
// CLI: `yoloai system check`. Distinct from Doctor (full capability
// report per backend/mode).
func (s *System) CheckPrerequisites(ctx context.Context, opts CheckPrerequisitesOptions) ([]CheckResult, error) {
	if opts.BackendType == "" {
		return nil, yoerrors.NewUsageError("Backend is required")
	}
	if opts.AgentType == "" {
		return nil, yoerrors.NewUsageError("Agent is required")
	}

	var results []CheckResult

	// 1. Backend connectivity.
	rt, backendErr := newRuntime(ctx, opts.BackendType, s.layout)
	if backendErr != nil {
		results = append(results,
			CheckResult{Name: "backend", OK: false, Message: backendErr.Error()},
			// Image check is moot when backend is unreachable; skip.
		)
	} else {
		results = append(results, CheckResult{Name: "backend", OK: true})
		// 2. Base image exists.
		results = append(results, s.checkImage(ctx, rt, string(opts.BackendType)))
	}

	// 3. Agent credentials.
	results = append(results, s.checkAgent(string(opts.AgentType)))

	// 4. Isolation prerequisites (only when --isolation is specified).
	if opts.Isolation != "" {
		results = append(results, s.checkIsolation(ctx, rt, opts.Isolation))
	}

	if rt != nil {
		_ = rt.Close()
	}
	return results, nil
}

// checkImage verifies the yoloai-base image is available on rt.
func (s *System) checkImage(ctx context.Context, rt runtime.Runtime, backend string) CheckResult {
	exists, err := rt.IsReady(ctx)
	switch {
	case err != nil:
		return CheckResult{Name: "image", OK: false, Message: err.Error()}
	case !exists:
		return CheckResult{Name: "image", OK: false, Message: fmt.Sprintf("yoloai-base image not found — run 'yoloai system build --backend %s'", backend)}
	}
	return CheckResult{Name: "image", OK: true}
}

// checkAgent verifies that at least one of the agent's API-key env vars is
// present in the client's host-environment snapshot (s.layout.Env). The library
// never reads os.Environ; credentials arrive as data via ClientCreateOptions.Env (§12).
func (s *System) checkAgent(name string) CheckResult {
	def := agent.GetAgent(name)
	switch {
	case def == nil:
		return CheckResult{Name: "agent", OK: false, Message: fmt.Sprintf("unknown agent %q", name)}
	case len(def.APIKeyEnvVars) == 0:
		return CheckResult{Name: "agent", OK: true, Message: fmt.Sprintf("agent %q requires no credentials", name)}
	}
	var found []string
	for _, key := range def.APIKeyEnvVars {
		if s.layout.Env[key] != "" {
			found = append(found, key)
		}
	}
	if len(found) == 0 {
		return CheckResult{
			Name:    "agent",
			OK:      false,
			Message: fmt.Sprintf("no credentials set for agent %q (need one of: %s)", name, strings.Join(def.APIKeyEnvVars, ", ")),
		}
	}
	return CheckResult{Name: "agent", OK: true, Message: "found: " + strings.Join(found, ", ")}
}

// checkIsolation runs the capability checks declared by the backend
// for the requested isolation mode. Returns OK when the backend has
// no requirements for the mode.
func (s *System) checkIsolation(ctx context.Context, rt runtime.Runtime, isolation runtime.IsolationMode) CheckResult {
	if rt == nil {
		return CheckResult{Name: "isolation", OK: false, Message: "backend unavailable; isolation check skipped"}
	}
	capList := runtime.RequiredCapabilitiesFor(rt, isolation)
	if len(capList) == 0 {
		return CheckResult{Name: "isolation", OK: true}
	}
	env := caps.DetectEnvironment()
	checkResults := caps.RunChecks(ctx, capList, env)
	if err := caps.FormatError(checkResults); err != nil {
		return CheckResult{Name: "isolation", OK: false, Message: err.Error()}
	}
	return CheckResult{Name: "isolation", OK: true}
}

// SystemPruneOptions configures System.Prune. Always operates across
// every backend that's currently available — per-backend pruning was
// dropped under Q-L as having no real-world use case.
type SystemPruneOptions struct {
	// DryRun reports what would be removed without removing it.
	DryRun bool
	// IncludeBaseImage additionally removes the backend's base/profile
	// images (forces yoloai-base to rebuild on next sandbox creation).
	// Build cache, volumes, and dangling images are always reclaimed —
	// even without this — because doing so forces no rebuild.
	IncludeBaseImage bool
	// Output receives line-oriented progress from underlying tools.
	// nil = io.Discard. Backend prune commands can be chatty; route
	// to stderr in interactive CLI usage.
	Output io.Writer
}

// PruneResult is what System.Prune returns.
//
//   - RemovedItems lists everything that was (or, under DryRun, would be)
//     removed: backend resources, stale temp dirs, never-initialized
//     sandbox dirs (PruneKindSandboxDir), and orphaned lock files
//     (PruneKindLockFile).
//   - Trashed lists sandbox dirs quarantined to the trash dir because
//     their metadata was unreadable but no recoverable work was detected.
//   - RefusedDataBearing lists broken sandbox dirs Prune left untouched
//     because recoverable user data was detected — the user must review
//     and remove them explicitly.
//   - TrashContents summarises the current trash dir so callers can tell
//     the user how much is recoverable (and reclaimable) there.
//
// The bulk path (Prune) only ever *removes* zero-stakes items; anything
// that might hold user data is refused-and-reported or quarantined, never
// silently deleted.
type PruneResult struct {
	RemovedItems       []PruneItem
	FreedBytes         int64 // best-effort; 0 when no backend reported byte counts
	Trashed            []TrashedSandbox
	RefusedDataBearing []RefusedSandbox
	TrashContents      TrashSummary
}

// PruneItem describes one removed (or removable) item.
type PruneItem struct {
	// Backend is the backend that owns the item, or empty for non-backend
	// items like temp dirs (Kind == PruneKindTempDir).
	BackendType BackendType
	// Kind classifies the resource type — see PruneKind* constants for
	// the shipping set. Open-set: backends can introduce new kinds.
	Kind PruneItemKind
	// Name is the resource identifier (e.g. "yoloai-mybox" for a
	// container, the Tart VM name for a VM, the temp-dir path).
	Name string
	// BytesReclaimed is the space freed by removing this item; 0 when the
	// backend can't report it.
	BytesReclaimed int64
}

// TrashedSandbox is a sandbox directory Prune quarantined to the trash
// dir because its metadata was unreadable/corrupt (or it was incomplete)
// but no recoverable work was detected. Recoverable with a plain `mv`.
type TrashedSandbox struct {
	Name         string
	OriginalPath string // original sandbox dir path
	TrashPath    string // path under the trash dir (empty under DryRun)
	Reason       string // why it was quarantined
}

// RefusedSandbox is a broken sandbox dir Prune deliberately left
// untouched because ProbeWorkData detected recoverable user data. The
// user reviews it (yoloai diff) and removes it explicitly (yoloai destroy).
type RefusedSandbox struct {
	Name   string
	Path   string
	Detail string // what data was detected
}

// TrashSummary counts the entries currently in the trash dir and their
// total size. Surfaced by Prune (and Doctor) so the user knows how much
// is recoverable — and reclaimable — there.
type TrashSummary struct {
	Count int
	Bytes int64
}

// staleTempFileAge is the threshold for considering yoloai temp dirs
// stale enough to remove during prune. Matches the CLI's previous
// behavior.
const staleTempFileAge = 1 * time.Hour

// Prune removes orphaned backend resources (sandbox containers with no
// matching sandbox dir on the host) and stale yoloai temp dirs across
// every available backend. Always reclaims each backend's no-rebuild cache
// (build cache, volumes, dangling images); with IncludeBaseImage, also removes
// base/profile images (forces yoloai-base to rebuild). DryRun reports what
// would be removed without removing.
func (s *System) Prune(ctx context.Context, opts SystemPruneOptions) (*PruneResult, error) {
	out := opts.Output
	if out == nil {
		out = io.Discard
	}
	known, broken := s.classifySandboxes()
	result := &PruneResult{}

	for _, desc := range runtime.Descriptors() {
		items, reclaimed := s.pruneBackend(ctx, desc.Type, known, opts, out)
		result.RemovedItems = append(result.RemovedItems, items...)
		result.FreedBytes += reclaimed
	}

	// Apply host-side sandbox-dir classifications.
	s.applyBrokenClassifications(broken, opts.DryRun, out, result)

	// Sweep orphaned <name>.lock files with no live holder. Runs after the
	// dir classifications above so locks beside a just-deleted never-init
	// dir are caught too.
	swept, err := store.SweepStaleLocks(s.layout, opts.DryRun)
	if err != nil {
		fmt.Fprintf(out, "Warning: lock sweep failed: %v\n", err) //nolint:errcheck // best-effort progress
	}
	for _, name := range swept {
		result.RemovedItems = append(result.RemovedItems, PruneItem{
			Kind: PruneKindLockFile, Name: name,
		})
	}

	tempItems, err := s.pruneTempFiles(opts.DryRun)
	result.RemovedItems = append(result.RemovedItems, tempItems...)
	if err != nil {
		return result, err
	}

	result.TrashContents = s.trashSummary()
	return result, nil
}

// applyBrokenClassifications carries out (or, under dryRun, records) the
// disposition for each broken sandbox dir: refuse-and-report, delete, or
// quarantine to trash. Per-entry failures are logged to out, not fatal.
func (s *System) applyBrokenClassifications(broken []classifiedSandbox, dryRun bool, out io.Writer, result *PruneResult) {
	for _, c := range broken {
		switch c.action {
		case actionRefuse:
			result.RefusedDataBearing = append(result.RefusedDataBearing, RefusedSandbox{
				Name: c.name, Path: c.path, Detail: c.detail,
			})
		case actionDelete:
			if !dryRun {
				if err := os.RemoveAll(c.path); err != nil {
					fmt.Fprintf(out, "Warning: remove %s failed: %v\n", c.path, err) //nolint:errcheck // best-effort progress
					continue
				}
			}
			result.RemovedItems = append(result.RemovedItems, PruneItem{
				Kind: PruneKindSandboxDir, Name: c.name,
			})
		case actionTrash:
			dest := c.path
			if !dryRun {
				moved, err := store.QuarantineSandbox(s.layout, c.name)
				if err != nil {
					fmt.Fprintf(out, "Warning: quarantine %s failed: %v\n", c.name, err) //nolint:errcheck // best-effort progress
					continue
				}
				dest = moved
			}
			result.Trashed = append(result.Trashed, TrashedSandbox{
				Name: c.name, OriginalPath: c.path, TrashPath: dest, Reason: c.detail,
			})
		}
	}
}

// pruneBackend handles one backend's scan + (optionally) execute + cache
// reclaim. Returns the items removed (or, under DryRun, that would be removed)
// and the bytes reclaimed by the cache prune. Cache pruning always runs: plain
// prune reclaims the build cache (no rebuild forced), and IncludeBaseImage also
// drops the base images. Per-backend failures are logged to opts.Output rather
// than aborting the whole prune.
func (s *System) pruneBackend(ctx context.Context, backend BackendType, known []string, opts SystemPruneOptions, out io.Writer) ([]PruneItem, int64) {
	rt, err := newRuntime(ctx, backend, s.layout)
	if err != nil {
		return nil, 0
	}
	defer rt.Close() //nolint:errcheck // best-effort

	scan, err := rt.Prune(ctx, known, true, out)
	if err != nil {
		fmt.Fprintf(out, "Warning: scan %s failed: %v\n", backend, err) //nolint:errcheck
		return nil, 0
	}

	var items []PruneItem
	if !opts.DryRun && len(scan.Items) > 0 {
		actual, pruneErr := rt.Prune(ctx, known, false, out)
		if pruneErr != nil {
			fmt.Fprintf(out, "Warning: prune %s failed: %v\n", backend, pruneErr) //nolint:errcheck
			return nil, 0
		}
		for _, item := range actual.Items {
			items = append(items, PruneItem{
				BackendType: backend,
				Kind:        PruneItemKind(item.Kind),
				Name:        item.Name,
			})
		}
	} else {
		for _, item := range scan.Items {
			items = append(items, PruneItem{
				BackendType: backend,
				Kind:        PruneItemKind(item.Kind),
				Name:        item.Name,
			})
		}
	}

	reclaimed, err := runtime.PruneCacheFor(ctx, rt, opts.IncludeBaseImage, opts.DryRun, out)
	if err != nil {
		fmt.Fprintf(out, "Warning: cache prune %s failed: %v\n", backend, err) //nolint:errcheck
	}
	return items, reclaimed
}

// pruneTempFiles scans (and, when !dryRun, removes) stale yoloai
// temp dirs. Returns the list of stale dirs as PruneItem entries.
func (s *System) pruneTempFiles(dryRun bool) ([]PruneItem, error) {
	stale, err := sandbox.PruneTempFiles(true, staleTempFileAge)
	if err != nil {
		return nil, fmt.Errorf("scan temp files: %w", err)
	}
	items := make([]PruneItem, 0, len(stale))
	for _, path := range stale {
		items = append(items, PruneItem{Kind: PruneKindTempDir, Name: path})
	}
	if !dryRun {
		if _, err := sandbox.PruneTempFiles(false, staleTempFileAge); err != nil {
			return items, fmt.Errorf("remove temp files: %w", err)
		}
	}
	return items, nil
}

// sandboxAction is the disposition classifySandboxes assigns to a broken
// sandbox dir (one whose metadata fails to load).
type sandboxAction int

const (
	// actionDelete: never-initialized dir with no recoverable work — safe
	// to delete (zero-stakes).
	actionDelete sandboxAction = iota
	// actionTrash: unreadable/corrupt metadata (or incomplete dir with
	// ambiguous content), no detectable work — quarantine to trash so the
	// user can recover it if it mattered (the decided safe default).
	actionTrash
	// actionRefuse: recoverable user data detected — leave untouched and
	// report so the user reviews + removes it explicitly.
	actionRefuse
)

// classifiedSandbox is one broken sandbox dir plus its disposition.
type classifiedSandbox struct {
	name   string
	path   string
	action sandboxAction
	detail string
}

// classifySandboxes reads DataDir/sandboxes/ and splits entries into
// known instances (meta loads → returned for backend orphan matching)
// and classified broken dirs (meta fails to load → an action chosen from
// the LoadMeta failure kind crossed with ProbeWorkData).
//
// The disposition matrix (recoverability, not "brokenness"):
//   - meta loads                                  → known (untouched)
//   - data detected (any failure kind)            → refuse + report
//   - missing meta + no work dir                  → delete (never-init)
//   - corrupt/version-too-new meta, no data,
//     or incomplete dir w/ ambiguous content      → quarantine to trash
func (s *System) classifySandboxes() (known []string, broken []classifiedSandbox) {
	dir := s.layout.SandboxesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)

		if _, loadErr := store.LoadEnvironment(path); loadErr == nil {
			known = append(known, store.InstanceName(s.layout.Principal, name))
			continue
		} else {
			state, detail := sandbox.ProbeWorkData(path)
			c := classifiedSandbox{name: name, path: path, detail: detail}
			switch {
			case state == sandbox.WorkDataPresent:
				c.action = actionRefuse
			case errors.Is(loadErr, os.ErrNotExist) && state == sandbox.WorkDataNone:
				c.action = actionDelete
				c.detail = "never initialized (no metadata, no work directory)"
			default:
				c.action = actionTrash
				if c.detail == "" {
					if errors.Is(loadErr, os.ErrNotExist) {
						c.detail = "incomplete sandbox (no metadata; unclassifiable content)"
					} else {
						c.detail = "unreadable or corrupt metadata"
					}
				}
			}
			broken = append(broken, c)
		}
	}
	return known, broken
}

// trashSummary reports the current trash-dir entry count and total size.
func (s *System) trashSummary() TrashSummary {
	dir := s.layout.TrashDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return TrashSummary{}
	}
	return TrashSummary{Count: len(entries), Bytes: dirSize(dir)}
}

// EmptyTrash deletes every entry in the trash dir, returning the number
// removed and bytes freed (best-effort). Non-interactive (Q-F): the CLI
// is responsible for confirming with the user before calling, since trash
// may hold data the user wanted.
func (s *System) EmptyTrash() (removed int, freed int64, err error) {
	dir := s.layout.TrashDir()
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read trash dir: %w", readErr)
	}
	freed = dirSize(dir)
	for _, entry := range entries {
		p := filepath.Join(dir, entry.Name())
		if rmErr := os.RemoveAll(p); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove %s: %w", p, rmErr))
			continue
		}
		removed++
	}
	return removed, freed, err
}

// profileHasDockerfile returns nil if the named profile or any of its
// ancestors carries a Dockerfile; *UsageError otherwise.
func (s *System) profileHasDockerfile(profile string) error {
	if config.ProfileHasDockerfile(s.layout, profile) {
		return nil
	}
	chain, err := config.ResolveProfileChain(s.layout, profile)
	if err != nil {
		return err
	}
	for _, name := range chain {
		if name != "base" && config.ProfileHasDockerfile(s.layout, name) {
			return nil
		}
	}
	return yoerrors.NewUsageError("profile %q has no Dockerfile (and no ancestor does either)", profile)
}

// dirSize sums every regular file under dir. Returns 0 on any
// error — best-effort, matches the CLI's existing semantics.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort walk
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
