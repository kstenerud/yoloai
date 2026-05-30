// ABOUTME: Tart's implementation of the AppleSimulatorRuntimes optional
// ABOUTME: interface — resolves user-facing runtime specs to a base image name.

package tart

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
)

// PrepareRuntimeBase implements runtime.AppleSimulatorRuntimes. It resolves
// `runtimeSpecs` (e.g. ["ios:26.4", "tvos"]) into a tart base-image name
// that has the requested simulator runtimes already installed, and returns
// the name. Returns a UsageError when the resolved base does not exist
// locally — yoloai system tart add is then the expected next step.
//
// Previously lived in sandbox/create.go's resolveRuntimeBase via an explicit
// type assertion to *tart.Runtime; relocated here as part of W-L7 so that
// sandbox/ no longer imports runtime/tart.
func (r *Runtime) PrepareRuntimeBase(ctx context.Context, layout config.Layout, runtimeSpecs []string) (string, error) {
	resolved, err := ResolveRuntimeVersions(runtimeSpecs)
	if err != nil {
		return "", fmt.Errorf("resolve runtimes: %w", err)
	}

	cacheKey := GenerateCacheKey(resolved)
	baseName := "yoloai-base-" + cacheKey

	release, err := AcquireBaseLock(layout, baseName)
	if err != nil {
		return "", fmt.Errorf("acquire base lock: %w", err)
	}
	defer release()

	exists, err := r.BaseExists(ctx, baseName)
	if err != nil {
		return "", fmt.Errorf("check base: %w", err)
	}
	if !exists {
		return "", runtimeBaseNotFoundError(baseName, runtimeSpecs, resolved)
	}
	return baseName, nil
}

// runtimeBaseNotFoundError builds the user-facing error when a requested
// tart runtime base is missing on this host.
func runtimeBaseNotFoundError(baseName string, runtimeSpecs []string, resolved []RuntimeVersion) error {
	runtimeDesc := FormatRuntimeList(resolved)
	attemptedSpecs := make([]string, 0, len(resolved))
	for _, rt := range resolved {
		attemptedSpecs = append(attemptedSpecs, fmt.Sprintf("%s:%s", rt.Platform, rt.Version))
	}
	return yoerrors.NewUsageError(
		"Runtime base '%s' not found.\n\n"+
			"Requested runtimes: %s\n"+
			"Resolved to: %s\n\n"+
			"To create this runtime base, run:\n"+
			"  yoloai system tart add %s\n\n"+
			"To see existing runtime bases:\n"+
			"  yoloai system tart list",
		baseName,
		strings.Join(runtimeSpecs, ", "),
		runtimeDesc,
		strings.Join(attemptedSpecs, " "),
	)
}
