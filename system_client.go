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

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	"github.com/kstenerud/yoloai/sandbox"
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
	Name   string
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

// BuildOptions configures SystemClient.Build.
type BuildOptions struct {
	// Profile is the profile name to build. Empty = base image only.
	// "base" is reserved and rejected (use Profile="" for the base image).
	Profile string
	// Backend selects the backend to build for. Empty = default
	// backend. Ignored when AllBackends is true.
	Backend string
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
		backend = resolveBackendFromConfig(ctx, s.layout)
	}
	return s.buildOne(ctx, backend, opts, out)
}

// buildOne runs one backend's build (base or profile) using a freshly
// constructed runtime that's closed before return.
func (s *SystemClient) buildOne(ctx context.Context, backend string, opts BuildOptions, out io.Writer) error {
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
	Backend string
	// Agent is the agent name whose credentials are checked. Required;
	// caller resolves the default before calling.
	Agent string
	// Isolation, when non-empty, triggers an isolation-mode capability
	// check via runtime.RequiredCapabilitiesFor + caps.RunChecks.
	Isolation string
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
		results = append(results, s.checkImage(ctx, rt, opts.Backend))
	}

	// 3. Agent credentials.
	results = append(results, s.checkAgent(opts.Agent))

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
		if os.Getenv(key) != "" {
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
func (s *SystemClient) checkIsolation(ctx context.Context, rt runtime.Runtime, isolation string) CheckResult {
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
