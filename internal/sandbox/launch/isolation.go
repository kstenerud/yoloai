// ABOUTME: CheckIsolationPrerequisites validates that the host satisfies an
// ABOUTME: isolation mode's required capabilities before create/reset proceeds.
package launch

import (
	"context"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
)

// CheckIsolationPrerequisites validates isolation prerequisites via RequiredCapabilities.
// Returns nil when all checks pass, or a formatted error listing missing prerequisites.
// Shared by the create pipeline (before the image build) and lifecycle reset
// (when changing an existing sandbox's isolation mode) — it lives in launch/ so
// neither create/ nor lifecycle/ needs to import the other.
func CheckIsolationPrerequisites(ctx context.Context, rt runtime.Backend, isolation runtime.IsolationMode) error {
	capList := runtime.RequiredCapabilitiesFor(rt, isolation)
	if len(capList) == 0 {
		return nil // backend has no requirements for this mode
	}
	env := caps.DetectEnvironment()
	results := caps.RunChecks(ctx, capList, env)
	return caps.FormatError(results)
}
