package caps

// ABOUTME: RunChecks, ComputeAvailability, and FormatError — the check driver and
// ABOUTME: error formatter for the capability registry. (Human-readable doctor
// ABOUTME: output is formatted in the doctorcmd package over the public read-model.)

import (
	"context"
	"fmt"
	"strings"
)

// RunChecks runs each capability's Check function and classifies each result
// as satisfied, fixable-failure, or permanent-failure.
// env is detected once by the caller and passed to Permanent and Fix.
func RunChecks(ctx context.Context, capList []HostCapability, env Environment) []CheckResult {
	results := make([]CheckResult, 0, len(capList))
	for _, cap := range capList {
		result := CheckResult{Cap: cap}
		if cap.Check != nil {
			result.Err = cap.Check(ctx)
		}
		if result.Err != nil {
			if cap.Permanent != nil && cap.Permanent(env) {
				result.IsPermanent = true
			} else if cap.Fix != nil {
				result.Steps = cap.Fix(env)
			}
		}
		results = append(results, result)
	}
	return results
}

// ComputeAvailability returns the aggregate availability of a result set:
// Unavailable if any check is permanent, NeedsSetup if any failed but all
// failures are fixable, Ready if all checks passed.
func ComputeAvailability(results []CheckResult) Availability {
	hasFailure := false
	hasPermanent := false
	for _, r := range results {
		if r.Err != nil {
			hasFailure = true
			if r.IsPermanent {
				hasPermanent = true
			}
		}
	}
	if hasPermanent {
		return Unavailable
	}
	if hasFailure {
		return NeedsSetup
	}
	return Ready
}

// FormatError returns a single error describing all failed checks, suitable
// for use in runtime error paths (e.g. sandbox creation). Returns nil if all
// checks passed.
func FormatError(results []CheckResult) error {
	var msgs []string
	for _, r := range results {
		if r.Err != nil {
			msgs = append(msgs, r.Cap.Summary+": "+r.Err.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("missing prerequisites:\n  - %s", strings.Join(msgs, "\n  - "))
}
