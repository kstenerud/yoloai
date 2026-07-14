package caps

// ABOUTME: RunChecks, ComputeAvailability, and FormatError — the check driver and
// ABOUTME: error formatter for the capability registry. (Human-readable doctor
// ABOUTME: output is formatted in the doctorcmd package over the public read-model.)

import (
	"context"
	"errors"
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
// checks passed. Each failure carries its Detail (why it's needed) and, when the
// failure is fixable, the remediation commands the capability provided — so the
// user sees how to fix it at the point of failure, not only via system doctor.
// (The doctor renders the same steps in richer form over the public read-model;
// this package sits below the CLI, so the two renderers are intentionally
// separate.)
func FormatError(results []CheckResult) error {
	var blocks []string
	for _, r := range results {
		if r.Err != nil && !r.Cap.Advisory {
			blocks = append(blocks, formatFailure(r))
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	return errors.New("missing prerequisites:\n\n" + strings.Join(blocks, "\n\n") +
		"\n\nrun 'yoloai system doctor' for full diagnostics")
}

// formatFailure renders one failed check: its summary + error, why it's needed,
// and either a permanence note or its fix steps.
func formatFailure(r CheckResult) string {
	lines := []string{"  - " + r.Cap.Summary + ": " + r.Err.Error()}
	if r.Cap.Detail != "" {
		lines = append(lines, "      "+r.Cap.Detail)
	}
	if r.IsPermanent {
		return strings.Join(append(lines, "      (cannot be resolved in the current environment)"), "\n")
	}
	return strings.Join(append(lines, fixStepLines(r.Steps)...), "\n")
}

// fixStepLines renders a capability's remediation steps. Returns nil when the
// capability offered no command-level guidance.
func fixStepLines(steps []FixStep) []string {
	if len(steps) == 0 {
		return nil
	}
	header := "      to fix:"
	if len(steps) > 1 {
		header = "      to fix (choose one):"
	}
	lines := []string{header}
	for _, s := range steps {
		desc := s.Description
		// The command usually already carries `sudo`; only flag root when it does not.
		if s.NeedsRoot && !strings.HasPrefix(strings.TrimSpace(s.Command), "sudo") {
			desc += " (requires root)"
		}
		lines = append(lines, "        - "+desc)
		for cmd := range strings.SplitSeq(s.Command, "\n") {
			if cmd != "" {
				lines = append(lines, "            "+cmd)
			}
		}
		if s.URL != "" {
			lines = append(lines, "            see: "+s.URL)
		}
	}
	return lines
}
