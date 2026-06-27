// ABOUTME: Public TartBases admin handle for managing Apple simulator runtime
// ABOUTME: base images (iOS/tvOS/watchOS/visionOS) on the Tart backend.
package yoloai

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	tartrt "github.com/kstenerud/yoloai/runtime/tart"
)

// baseNamePrefix is the VM-name prefix for yoloai-managed Tart runtime bases.
const baseNamePrefix = "yoloai-base"

// TartRuntimeVersion is a resolved Apple simulator runtime: a platform and the
// concrete version (with build identifier for tie-breaking). Public mirror of
// the Tart backend's internal runtime-version type.
type TartRuntimeVersion struct {
	Platform string // "ios", "tvos", "watchos", "visionos"
	Version  string // e.g. "26.2"
	Build    string // build identifier, for tie-breaking
}

// TartBaseInfo describes one Tart runtime base image (a yoloai-base-* VM).
type TartBaseInfo struct {
	Name      string // full VM name, e.g. "yoloai-base-ios-26.4"
	CacheKey  string // platform/version key from Name; "" for the bare base
	SizeBytes int64  // on-disk size (0 when not yet known, e.g. just created)
}

// TartBasePlan is what Add will build: the resolved runtimes and the VM name
// they hash to. Produced by PlanBase so a caller can preview/confirm before
// committing to the (slow) create.
type TartBasePlan struct {
	Name     string
	Runtimes []TartRuntimeVersion
}

// TartBaseExistsError is returned by Add when a base with the planned name
// already exists.
type TartBaseExistsError struct{ Name string }

func (e *TartBaseExistsError) Error() string {
	return fmt.Sprintf("tart runtime base %q already exists", e.Name)
}

// TartBaseNotFoundError is returned by Remove when the named base does not
// exist.
type TartBaseNotFoundError struct{ Name string }

func (e *TartBaseNotFoundError) Error() string {
	return fmt.Sprintf("tart runtime base %q not found", e.Name)
}

// TartBaseAdmin manages Tart runtime base images. Obtain one via
// System.TartBases. The mechanism reports facts (availability, runtimes,
// bases) and performs the create/delete; deciding whether the Tart backend is
// appropriate is the caller's policy (development-principles.md §2).
type TartBaseAdmin struct {
	layout config.Layout
}

// Available reports whether the Tart backend can be constructed in this
// environment. Returns (true, nil) when it can; (false, err) with the
// construction failure otherwise (e.g. non-macOS host, tart not installed).
func (a *TartBaseAdmin) Available(ctx context.Context) (bool, error) {
	r, err := runtime.New(ctx, runtime.BackendTart, a.layout)
	if err != nil {
		return false, err
	}
	_ = r.Close()
	return true, nil
}

// AvailableRuntimes queries the host (xcrun simctl) for installed simulator
// runtimes that a base can be built from. Needs no Tart VM connection.
func (a *TartBaseAdmin) AvailableRuntimes(ctx context.Context) ([]TartRuntimeVersion, error) {
	env := tartrt.BaseAdminEnv(a.layout)
	rv, err := tartrt.QueryAvailableRuntimes(ctx, env)
	if err != nil {
		return nil, err
	}
	return tartVersionsToPublic(rv), nil
}

// PlanBase resolves the given platform specs ("ios", "ios:26.4", …) against the
// host's available simulator runtimes and returns the resolved runtimes plus
// the VM name Add would create — without creating anything.
func (a *TartBaseAdmin) PlanBase(ctx context.Context, specs []string) (TartBasePlan, error) {
	env := tartrt.BaseAdminEnv(a.layout)
	resolved, err := tartrt.ResolveRuntimeVersions(ctx, env, specs)
	if err != nil {
		return TartBasePlan{}, err
	}
	return TartBasePlan{
		Name:     baseNamePrefix + "-" + tartrt.GenerateCacheKey(resolved),
		Runtimes: tartVersionsToPublic(resolved),
	}, nil
}

// List returns all yoloai-managed Tart runtime base images.
func (a *TartBaseAdmin) List(ctx context.Context) ([]TartBaseInfo, error) {
	r, closeRT, err := a.open(ctx)
	if err != nil {
		return nil, err
	}
	defer closeRT()
	return listTartBases(ctx, r)
}

// Add builds the runtime base described by plan. It is atomic with respect to
// the create: it re-checks existence under the base lock before building.
// Returns *TartBaseExistsError if a base with plan.Name already exists.
// Build progress (a slow, minutes-long operation) is written to progress; pass
// nil to discard it. The library never writes to the process's os.Stdout (§12)
// — the CLI passes cmd.OutOrStdout(), a daemon passes its own writer.
func (a *TartBaseAdmin) Add(ctx context.Context, plan TartBasePlan, progress io.Writer) (TartBaseInfo, error) {
	if progress == nil {
		progress = io.Discard
	}
	r, closeRT, err := a.open(ctx)
	if err != nil {
		return TartBaseInfo{}, err
	}
	defer closeRT()

	release, err := tartrt.AcquireBaseLock(a.layout, plan.Name)
	if err != nil {
		return TartBaseInfo{}, fmt.Errorf("acquire base lock: %w", err)
	}
	defer release()

	exists, err := r.BaseExists(ctx, plan.Name)
	if err != nil {
		return TartBaseInfo{}, fmt.Errorf("check base: %w", err)
	}
	if exists {
		return TartBaseInfo{}, &TartBaseExistsError{Name: plan.Name}
	}

	if err := r.CreateBase(ctx, plan.Name, tartVersionsToInternal(plan.Runtimes), progress); err != nil {
		return TartBaseInfo{}, fmt.Errorf("create base: %w", err)
	}

	return TartBaseInfo{Name: plan.Name, CacheKey: cacheKeyFromBaseName(plan.Name)}, nil
}

// Remove deletes the named runtime base and returns the bytes it freed.
// Returns *TartBaseNotFoundError if no such base exists.
func (a *TartBaseAdmin) Remove(ctx context.Context, name string) (freed int64, err error) {
	r, closeRT, err := a.open(ctx)
	if err != nil {
		return 0, err
	}
	defer closeRT()

	bases, err := listTartBases(ctx, r)
	if err != nil {
		return 0, fmt.Errorf("list bases: %w", err)
	}
	var size int64
	found := false
	for _, b := range bases {
		if b.Name == name {
			size, found = b.SizeBytes, true
			break
		}
	}
	if !found {
		return 0, &TartBaseNotFoundError{Name: name}
	}

	if err := r.DeleteVM(ctx, name); err != nil {
		return 0, fmt.Errorf("delete base: %w", err)
	}
	return size, nil
}

// open constructs the Tart runtime and returns it with a close func.
func (a *TartBaseAdmin) open(ctx context.Context) (*tartrt.Runtime, func(), error) {
	r, err := runtime.New(ctx, runtime.BackendTart, a.layout)
	if err != nil {
		return nil, nil, err
	}
	tr, ok := r.(*tartrt.Runtime)
	if !ok {
		// runtime.New(BackendTart) returns *tart.Runtime by construction; a
		// different type means the registry is wired with the wrong factory —
		// a programming bug, so panic with the type rather than return.
		_ = r.Close()
		panic(fmt.Sprintf("yoloai bug: runtime.New(BackendTart) returned %T, not *tart.Runtime", r))
	}
	return tr, func() { _ = r.Close() }, nil
}

// listTartBases enumerates yoloai-base-* VMs on an already-open runtime.
func listTartBases(ctx context.Context, r *tartrt.Runtime) ([]TartBaseInfo, error) {
	entries, err := r.ListVMs(ctx)
	if err != nil {
		return nil, err
	}
	bases := make([]TartBaseInfo, 0)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, baseNamePrefix) {
			continue
		}
		bases = append(bases, TartBaseInfo{
			Name:      e.Name,
			CacheKey:  cacheKeyFromBaseName(e.Name),
			SizeBytes: e.Size,
		})
	}
	return bases, nil
}

// cacheKeyFromBaseName extracts the platform/version key from a base VM name.
// The bare "yoloai-base" (no runtimes) has an empty key.
func cacheKeyFromBaseName(name string) string {
	if name == baseNamePrefix {
		return ""
	}
	return strings.TrimPrefix(name, baseNamePrefix+"-")
}

func tartVersionsToPublic(in []tartrt.RuntimeVersion) []TartRuntimeVersion {
	out := make([]TartRuntimeVersion, len(in))
	for i, rv := range in {
		out[i] = TartRuntimeVersion{Platform: rv.Platform, Version: rv.Version, Build: rv.Build}
	}
	return out
}

func tartVersionsToInternal(in []TartRuntimeVersion) []tartrt.RuntimeVersion {
	if in == nil {
		return nil
	}
	out := make([]tartrt.RuntimeVersion, len(in))
	for i, rv := range in {
		out[i] = tartrt.RuntimeVersion{Platform: rv.Platform, Version: rv.Version, Build: rv.Build}
	}
	return out
}
