// ABOUTME: SystemClient — admin sub-client off Client. Hosts cross-backend
// ABOUTME: operations (disk usage, prune, build, check) that are scoped to the
// ABOUTME: host rather than to a specific sandbox.
package yoloai

import (
	"context"
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
)

// SystemClient scopes `yoloai system …` operations. Constructed via
// Client.System() (for embedders that already have a Client) or
// directly via NewSystemClient (for the CLI and embedders that
// only need admin ops). Never errors at construction.
//
// Decoupled from a specific backend on purpose: cross-backend
// methods (DiskUsage, Prune, Build with AllBackends) iterate every
// registered backend that's available in the current environment
// and spin up an ephemeral runtime per backend. Single-backend
// methods (Check, single-backend Build) take a BackendName parameter.
//
// Safe for concurrent use by multiple goroutines. Read-only methods
// (DiskUsage, Check) run in parallel. Write methods (Build, Prune)
// acquire backend-internal locks where applicable.
type SystemClient struct {
	layout config.Layout
}

// NewSystemClient constructs a SystemClient from a layout. Used by
// the CLI's system_* commands (which don't have a backend-specific
// Client) and by embedders that need only admin operations.
func NewSystemClient(layout config.Layout) *SystemClient {
	return &SystemClient{layout: layout}
}

// System returns the admin sub-client for system-level operations.
// Always non-nil; never errors. See SystemClient for the surface.
func (c *Client) System() *SystemClient {
	return &SystemClient{layout: c.layout}
}

// DiskUsage reports total on-disk usage by yoloai and each available
// backend. Walks the sandboxes directory for yoloai's own footprint
// and queries each backend's CacheUsage. Backends that fail to report
// surface their error in the per-backend entry rather than aborting
// the whole call.
type DiskUsage struct {
	// Sandboxes is the total byte count under DataDir/sandboxes/.
	Sandboxes int64
	// PerBackend has one entry per backend that was probed available.
	// Order matches runtime.Descriptors() (registration order).
	PerBackend []BackendDiskUsage
}

// BackendDiskUsage is one row of DiskUsage's per-backend section.
// When Err is non-nil, Bytes is 0 and Detail carries any partial
// progress info from the backend.
type BackendDiskUsage struct {
	Name   BackendName
	Bytes  int64
	Detail string
	Err    error
}

// DiskUsage returns a per-backend disk-usage snapshot plus yoloai's
// own sandboxes-directory size. Unavailable backends are skipped.
func (s *SystemClient) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	du := &DiskUsage{
		Sandboxes: dirSize(s.layout.SandboxesDir()),
	}
	for _, desc := range runtime.Descriptors() {
		rt, err := newRuntime(ctx, desc.Name, s.layout)
		if err != nil {
			// Backend not available in this environment — skip silently.
			// The CLI's `yoloai system disk` does the same filtering via
			// checkBackend before calling per-backend code.
			continue
		}
		usage, usageErr := runtime.CacheUsageFor(ctx, rt)
		_ = rt.Close()
		du.PerBackend = append(du.PerBackend, BackendDiskUsage{
			Name:   desc.Name,
			Bytes:  usage.BytesUsed,
			Detail: usage.Detail,
			Err:    usageErr,
		})
	}
	return du, nil
}

// BackendStatus reports whether a registered backend is usable in the current
// environment. Note explains why when Available is false.
type BackendStatus struct {
	Name      BackendName
	Available bool
	Note      string // failure reason when Available is false; empty otherwise
}

// SystemInfo describes a yoloai installation: where its state lives and which
// backends are usable. Build metadata (version/commit/date) is the CLI's
// concern and is intentionally not included. Disk usage is a separate (slower)
// call — see DiskUsage.
type SystemInfo struct {
	DataDir        string
	SandboxesDir   string
	GlobalConfig   string // path to the global config.yaml
	DefaultsConfig string // path to the defaults config.yaml
	Backends       []BackendStatus
}

// Backends probes every registered backend's availability by constructing it
// and immediately closing it. A backend that fails to construct (missing
// daemon, unsupported platform, …) is reported Available=false with the reason
// in Note. Order matches runtime.Descriptors() (registration order).
func (s *SystemClient) Backends(ctx context.Context) []BackendStatus {
	descs := runtime.Descriptors()
	out := make([]BackendStatus, 0, len(descs))
	for _, desc := range descs {
		st := BackendStatus{Name: desc.Name, Available: true}
		rt, err := newRuntime(ctx, desc.Name, s.layout)
		if err != nil {
			st.Available = false
			st.Note = err.Error()
		} else {
			_ = rt.Close() //nolint:errcheck // best-effort close after a probe
		}
		out = append(out, st)
	}
	return out
}

// ListAcrossBackends enumerates sandboxes across every backend that currently
// has sandbox state, inspecting each via its own backend. Returns the sandbox
// infos plus the names of backends that have sandbox dirs but couldn't be
// reached (e.g. their daemon is down) so callers can warn without failing.
func (s *SystemClient) ListAcrossBackends(ctx context.Context) ([]*Info, []BackendName, error) {
	infos, unavailable, err := sandbox.ListSandboxesMultiBackend(ctx, s.layout,
		func(ctx context.Context, backend runtime.BackendName) (runtime.Runtime, error) {
			return newRuntime(ctx, backend, s.layout)
		})
	if err != nil {
		return nil, nil, err
	}
	unavailableNames := make([]BackendName, len(unavailable))
	for i, name := range unavailable {
		unavailableNames[i] = BackendName(name)
	}
	return infos, unavailableNames, nil
}

// Info returns the installation's paths and per-backend availability in one
// call. It never returns an error today (per-backend probe failures are
// captured in BackendStatus.Note); the error return is kept for forward
// compatibility, mirroring DiskUsage.
func (s *SystemClient) Info(ctx context.Context) (*SystemInfo, error) {
	return &SystemInfo{
		DataDir:        s.layout.YoloaiDir(),
		SandboxesDir:   s.layout.SandboxesDir(),
		GlobalConfig:   s.layout.GlobalConfigPath(),
		DefaultsConfig: s.layout.DefaultsConfigPath(),
		Backends:       s.Backends(ctx),
	}, nil
}

// BackendReport is one backend's diagnostic report from Doctor — its base-mode
// availability plus a per-isolation-mode capability check breakdown.
// Re-exported (type alias) from internal/runtime/caps.
type BackendReport = caps.BackendReport

// DoctorOptions filters Doctor's per-backend health checks. Empty filters
// (the zero value) report every backend and every isolation mode.
type DoctorOptions struct {
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
func (s *SystemClient) Doctor(ctx context.Context, opts DoctorOptions) ([]BackendReport, error) {
	env := caps.DetectEnvironment()
	var reports []BackendReport
	for _, desc := range runtime.Descriptors() {
		if opts.BackendFilter != "" && string(desc.Name) != opts.BackendFilter {
			continue
		}
		reports = append(reports, s.backendReports(ctx, desc.Name, env, opts.IsolationFilter)...)
	}
	return reports, nil
}

// backendReports builds the report rows for a single backend: an init-failure
// row if it can't be constructed, otherwise a base-mode row (unless filtered)
// plus one row per matching supported isolation mode.
func (s *SystemClient) backendReports(ctx context.Context, backend BackendName, env caps.Environment, isolationFilter string) []BackendReport {
	rt, err := newRuntime(ctx, backend, s.layout)
	if err != nil {
		if isolationFilter != "" {
			return nil // an unavailable backend has no isolation-mode rows to filter to
		}
		return []BackendReport{{
			Backend: string(backend), Mode: "?", IsBaseMode: true,
			InitErr: err, Availability: caps.Unavailable,
		}}
	}
	defer rt.Close() //nolint:errcheck // best-effort close after probing

	var reports []BackendReport
	if isolationFilter == "" {
		reports = append(reports, BackendReport{
			Backend: string(backend), Mode: string(rt.Descriptor().BaseModeName),
			IsBaseMode: true, Availability: caps.Ready,
		})
	}
	for _, mode := range rt.Descriptor().SupportedIsolationModes {
		if isolationFilter != "" && string(mode) != isolationFilter {
			continue
		}
		results := caps.RunChecks(ctx, runtime.RequiredCapabilitiesFor(rt, mode), env)
		reports = append(reports, BackendReport{
			Backend: string(backend), Mode: string(mode), IsBaseMode: false,
			Results: results, Availability: caps.ComputeAvailability(results),
		})
	}
	return reports
}

// BuildOptions configures SystemClient.Build.
type BuildOptions struct {
	// Profile is the profile name to build. Empty = base image only.
	// "base" is reserved and rejected (use Profile="" for the base image).
	Profile string
	// Backend selects the backend to build for. Empty = default
	// backend. Ignored when AllBackends is true.
	Backend BackendName
	// AllBackends builds across every backend that's currently
	// available. Mutually exclusive with Backend.
	AllBackends bool
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

// Build builds the base image (Profile == "") or a profile image
// (Profile != "") for one backend or all available backends. Returns
// the first error from any backend; later backends in the iteration
// are skipped.
func (s *SystemClient) Build(ctx context.Context, opts BuildOptions) error {
	if opts.AllBackends && opts.Backend != "" {
		return sandbox.NewUsageError("Backend and AllBackends are mutually exclusive")
	}
	if opts.Profile != "" {
		if err := config.ValidateProfileName(opts.Profile); err != nil {
			return err
		}
		if !config.ProfileExists(s.layout, opts.Profile) {
			return sandbox.NewUsageError("profile %q does not exist", opts.Profile)
		}
		if err := s.profileHasDockerfile(opts.Profile); err != nil {
			return err
		}
	} else if len(opts.Secrets) > 0 {
		return sandbox.NewUsageError("Secrets is only supported with a non-empty Profile")
	}

	out := opts.Output
	if out == nil {
		out = io.Discard
	}

	if opts.AllBackends {
		var built int
		for _, desc := range runtime.Descriptors() {
			if err := s.buildOne(ctx, desc.Name, opts, out); err != nil {
				// Stop on first failure — matches the CLI's existing
				// behavior. A more permissive policy can be added if
				// users want best-effort multi-backend builds.
				return fmt.Errorf("build %s: %w", desc.Name, err)
			}
			built++
		}
		if built == 0 {
			return fmt.Errorf("no available backends to build for")
		}
		return nil
	}

	backend := opts.Backend
	if backend == "" {
		// Build targets the container slot — no isolation/OS routing.
		backend = resolveBackendFromConfig(ctx, s.layout)
	}
	return s.buildOne(ctx, backend, opts, out)
}

// buildOne runs one backend's build (base or profile) using a freshly
// constructed runtime that's closed before return.
func (s *SystemClient) buildOne(ctx context.Context, backend BackendName, opts BuildOptions, out io.Writer) error {
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

// CheckOptions configures SystemClient.Check.
type CheckOptions struct {
	// Backend is the backend to verify. Required.
	Backend BackendName
	// Agent is the agent name whose credentials are checked. Required;
	// caller resolves the default before calling.
	Agent AgentName
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

// Check verifies that yoloai's prerequisites are satisfied. Runs all
// configured checks (always returns a result per check) and returns
// the result list. The error return is non-nil only for system-level
// failures (e.g. unknown backend); per-check failures are reflected
// in CheckResult.OK = false.
//
// CLI: `yoloai system check`. Distinct from Doctor (full capability
// report per backend/mode).
func (s *SystemClient) Check(ctx context.Context, opts CheckOptions) ([]CheckResult, error) {
	if opts.Backend == "" {
		return nil, sandbox.NewUsageError("Backend is required")
	}
	if opts.Agent == "" {
		return nil, sandbox.NewUsageError("Agent is required")
	}

	var results []CheckResult

	// 1. Backend connectivity.
	rt, backendErr := newRuntime(ctx, opts.Backend, s.layout)
	if backendErr != nil {
		results = append(results,
			CheckResult{Name: "backend", OK: false, Message: backendErr.Error()},
			// Image check is moot when backend is unreachable; skip.
		)
	} else {
		results = append(results, CheckResult{Name: "backend", OK: true})
		// 2. Base image exists.
		results = append(results, s.checkImage(ctx, rt, string(opts.Backend)))
	}

	// 3. Agent credentials.
	results = append(results, s.checkAgent(string(opts.Agent)))

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
func (s *SystemClient) checkImage(ctx context.Context, rt runtime.Runtime, backend string) CheckResult {
	exists, err := rt.IsReady(ctx)
	switch {
	case err != nil:
		return CheckResult{Name: "image", OK: false, Message: err.Error()}
	case !exists:
		return CheckResult{Name: "image", OK: false, Message: fmt.Sprintf("yoloai-base image not found — run 'yoloai system build --backend %s'", backend)}
	}
	return CheckResult{Name: "image", OK: true}
}

// checkAgent verifies that at least one of the agent's API-key env
// vars is set. Uses os.Getenv on agent.Definition.APIKeyEnvVars —
// the documented §12 exception (development-principles.md §12).
func (s *SystemClient) checkAgent(name string) CheckResult {
	def := agent.GetAgent(name)
	switch {
	case def == nil:
		return CheckResult{Name: "agent", OK: false, Message: fmt.Sprintf("unknown agent %q", name)}
	case len(def.APIKeyEnvVars) == 0:
		return CheckResult{Name: "agent", OK: true, Message: fmt.Sprintf("agent %q requires no credentials", name)}
	}
	var found []string
	for _, key := range def.APIKeyEnvVars {
		if os.Getenv(key) != "" { //nolint:forbidigo // §12: agent API-key presence check (declared exception)
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
func (s *SystemClient) checkIsolation(ctx context.Context, rt runtime.Runtime, isolation runtime.IsolationMode) CheckResult {
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

// PruneOptions configures SystemClient.Prune. Always operates across
// every backend that's currently available — per-backend pruning was
// dropped under Q-L as having no real-world use case.
type PruneOptions struct {
	// DryRun reports what would be removed without removing it.
	DryRun bool
	// IncludeBaseImage also reclaims the backend's image cache,
	// snapshots, volumes, and build cache (forces yoloai-base to
	// rebuild on next sandbox creation).
	IncludeBaseImage bool
	// Output receives line-oriented progress from underlying tools.
	// nil = io.Discard. Backend prune commands can be chatty; route
	// to stderr in interactive CLI usage.
	Output io.Writer
}

// PruneResult is what SystemClient.Prune returns. RemovedItems lists
// every backend resource and temp dir that was (or, under DryRun,
// would be) removed. BrokenSandboxes is informational: sandbox dirs
// that exist but can't load metadata; Prune does not touch them.
type PruneResult struct {
	RemovedItems    []PruneItem
	FreedBytes      int64 // best-effort; 0 when no backend reported byte counts
	BrokenSandboxes []BrokenSandbox
}

// PruneItem describes one removed (or removable) item.
type PruneItem struct {
	// Backend is the backend that owns the item, or empty for non-backend
	// items like temp dirs (Kind == PruneKindTempDir).
	Backend BackendName
	// Kind classifies the resource type — see PruneKind* constants for
	// the shipping set. Open-set: backends can introduce new kinds.
	Kind PruneItemKind
	// Name is the resource identifier (e.g. "yoloai-mybox" for a
	// container, the Tart VM name for a VM, the temp-dir path).
	Name string
	// Bytes reclaimed; 0 when the backend can't report.
	Bytes int64
}

// BrokenSandbox is an entry in DataDir/sandboxes/ whose meta.json
// can't be loaded. Surfaced informationally by Prune so the user can
// clean it up via `yoloai destroy <name>`.
type BrokenSandbox struct {
	Name string
	Path string
}

// staleTempFileAge is the threshold for considering yoloai temp dirs
// stale enough to remove during prune. Matches the CLI's previous
// behavior.
const staleTempFileAge = 1 * time.Hour

// Prune removes orphaned backend resources (sandbox containers with no
// matching sandbox dir on the host) and stale yoloai temp dirs across
// every available backend. With IncludeBaseImage, also reclaims each
// backend's image cache + snapshots + build cache (forces yoloai-base
// to rebuild). DryRun reports what would be removed without removing.
func (s *SystemClient) Prune(ctx context.Context, opts PruneOptions) (*PruneResult, error) {
	out := opts.Output
	if out == nil {
		out = io.Discard
	}
	known, broken := s.scanSandboxes()
	result := &PruneResult{BrokenSandboxes: broken}

	for _, desc := range runtime.Descriptors() {
		items := s.pruneBackend(ctx, desc.Name, known, opts, out)
		result.RemovedItems = append(result.RemovedItems, items...)
	}

	tempItems, err := s.pruneTempFiles(opts.DryRun)
	result.RemovedItems = append(result.RemovedItems, tempItems...)
	if err != nil {
		return result, err
	}
	return result, nil
}

// pruneBackend handles one backend's scan + (optionally) execute +
// optional cache reclaim. Returns the items removed (or, under
// DryRun, that would be removed). Per-backend failures are logged
// to opts.Output rather than aborting the whole prune.
func (s *SystemClient) pruneBackend(ctx context.Context, backend BackendName, known []string, opts PruneOptions, out io.Writer) []PruneItem {
	rt, err := newRuntime(ctx, backend, s.layout)
	if err != nil {
		return nil
	}
	defer rt.Close() //nolint:errcheck // best-effort

	scan, err := rt.Prune(ctx, known, true, out)
	if err != nil {
		fmt.Fprintf(out, "Warning: scan %s failed: %v\n", backend, err) //nolint:errcheck
		return nil
	}

	var items []PruneItem
	if !opts.DryRun && len(scan.Items) > 0 {
		actual, pruneErr := rt.Prune(ctx, known, false, out)
		if pruneErr != nil {
			fmt.Fprintf(out, "Warning: prune %s failed: %v\n", backend, pruneErr) //nolint:errcheck
			return nil
		}
		for _, item := range actual.Items {
			items = append(items, PruneItem{
				Backend: BackendName(backend),
				Kind:    PruneItemKind(item.Kind),
				Name:    item.Name,
			})
		}
	} else {
		for _, item := range scan.Items {
			items = append(items, PruneItem{
				Backend: BackendName(backend),
				Kind:    PruneItemKind(item.Kind),
				Name:    item.Name,
			})
		}
	}

	if opts.IncludeBaseImage {
		if err := runtime.PruneCacheFor(ctx, rt, opts.DryRun, out); err != nil {
			fmt.Fprintf(out, "Warning: cache prune %s failed: %v\n", backend, err) //nolint:errcheck
		}
	}
	return items
}

// pruneTempFiles scans (and, when !dryRun, removes) stale yoloai
// temp dirs. Returns the list of stale dirs as PruneItem entries.
func (s *SystemClient) pruneTempFiles(dryRun bool) ([]PruneItem, error) {
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

// SetupOptions configures SystemClient.Setup. Q-F: pure data — the
// interactive wizard lives in the CLI and fills these in by prompting
// the user (or accepting --flag overrides), then calls Setup. Setup
// itself never prompts.
type SetupOptions struct {
	// TmuxConf is the tmux config mode. REQUIRED. One of:
	// "default", "default+host", "host", "none".
	TmuxConf string
	// Backend is the default backend name (yoloai.BackendDocker,
	// yoloai.BackendTart, …). May be empty only when there's exactly
	// one (or zero) available backends on the platform — Setup
	// auto-picks in that case.
	Backend BackendName
	// Agent is the default agent name (yoloai.AgentClaude, …). May
	// be empty only when there's exactly one (or zero) available
	// agents.
	Agent AgentName
}

// SetupStatus is the host inspection a setup wizard needs to render
// its prompts. Re-exports sandbox.SetupStatus so CLI/embedder code
// can stay on the yoloai package boundary.
type SetupStatus = sandbox.SetupStatus

// SetupChoice is one option in a wizard prompt (backend or agent).
type SetupChoice = sandbox.SetupChoice

// TmuxConfigClass tells the wizard which prompt copy to use for the
// tmux question.
type TmuxConfigClass = sandbox.TmuxConfigClass

// Re-export the TmuxConfigClass constants so wizard code (in the CLI
// or external embedders) can switch on them without importing sandbox.
const (
	TmuxConfigNone  = sandbox.TmuxConfigNone
	TmuxConfigSmall = sandbox.TmuxConfigSmall
	TmuxConfigLarge = sandbox.TmuxConfigLarge
)

// SetupStatus inspects the host (reads ~/.tmux.conf, enumerates
// backends/agents) and returns the data a setup wizard needs to ask
// the user. Pure inspection — does not modify any config.
func (s *SystemClient) SetupStatus(ctx context.Context) (*SetupStatus, error) {
	_ = ctx
	// SetupStatus is pure inspection — no prompts — so the Manager never
	// reads its input. Pass an empty reader rather than reaching for
	// os.Stdin (§12: no ambient process state in the library).
	mgr := sandbox.NewManager(nil, slog.Default(), strings.NewReader(""), io.Discard, sandbox.WithLayout(s.layout))
	return mgr.SetupStatus(), nil
}

// Setup writes the user's setup answers to the config files under
// DataDir. Non-interactive: callers (CLI wizard or scripted setup)
// must supply every required answer.
//
// Returns *UsageError when:
//   - opts.TmuxConf is empty or not one of "default" / "default+host" /
//     "host" / "none".
//   - opts.Backend is empty when multiple backends are available
//     (use SetupStatus to discover them and prompt the user).
//   - opts.Agent is empty when multiple agents are available.
//   - opts.Backend or opts.Agent names an unknown value.
func (s *SystemClient) Setup(ctx context.Context, opts SetupOptions) error {
	// Setup is non-interactive — every answer comes from opts — so the
	// Manager never reads its input. Empty reader, not os.Stdin (§12).
	mgr := sandbox.NewManager(nil, slog.Default(), strings.NewReader(""), io.Discard, sandbox.WithLayout(s.layout))
	return mgr.ApplySetup(ctx, sandbox.SetupOptions{
		TmuxConf: opts.TmuxConf,
		Backend:  string(opts.Backend),
		Agent:    string(opts.Agent),
	})
}

// scanSandboxes reads DataDir/sandboxes/ and classifies entries:
// loadable meta.json → known instance; load failure → broken sandbox.
func (s *SystemClient) scanSandboxes() (known []string, broken []BrokenSandbox) {
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
		if _, err := store.LoadMeta(path); err != nil {
			broken = append(broken, BrokenSandbox{Name: name, Path: path})
		} else {
			known = append(known, store.InstanceName(name))
		}
	}
	return known, broken
}

// profileHasDockerfile returns nil if the named profile or any of its
// ancestors carries a Dockerfile; *UsageError otherwise.
func (s *SystemClient) profileHasDockerfile(profile string) error {
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
	return sandbox.NewUsageError("profile %q has no Dockerfile (and no ancestor does either)", profile)
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
