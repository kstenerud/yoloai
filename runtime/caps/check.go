package caps

// ABOUTME: RunChecks, ComputeAvailability, FormatError, and FormatDoctor — the check driver
// ABOUTME: and output formatters for the capability registry.

import (
	"context"
	"fmt"
	"io"
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

// FormatDoctor writes the full three-tier summary table followed by per-failure
// fix details to w. Takes a slice of BackendReport covering all (backend, mode)
// combinations, including base modes. Handles empty slices gracefully.
// For Unavailable entries, all failed checks are shown with fix steps where
// available — permanent failures are labelled as such.
func FormatDoctor(w io.Writer, reports []BackendReport) {
	if len(reports) == 0 {
		fmt.Fprintln(w, "No backends available to check.") //nolint:errcheck
		return
	}

	// Partition into three tiers.
	var ready, needsSetup, unavailable []BackendReport
	for _, r := range reports {
		switch r.Availability {
		case Ready:
			ready = append(ready, r)
		case NeedsSetup:
			needsSetup = append(needsSetup, r)
		default:
			unavailable = append(unavailable, r)
		}
	}

	// Print summary table.
	if len(ready) > 0 {
		fmt.Fprintln(w, "\nReady to use:") //nolint:errcheck
		for _, r := range ready {
			label := modeLabel(r)
			fmt.Fprintf(w, "  %-16s %s\n", r.Backend, label) //nolint:errcheck
		}
	}

	if len(needsSetup) > 0 {
		fmt.Fprintln(w, "\nNeeds setup:") //nolint:errcheck
		for _, r := range needsSetup {
			label := modeLabel(r)
			failing := countFailing(r.Results)
			total := len(r.Results)
			fmt.Fprintf(w, "  %-16s %-24s %d of %d checks failing\n", r.Backend, label, failing, total) //nolint:errcheck
		}
	}

	if len(unavailable) > 0 {
		fmt.Fprintln(w, "\nNot available on this machine:") //nolint:errcheck
		for _, r := range unavailable {
			label := modeLabel(r)
			reason := unavailableReason(r)
			fmt.Fprintf(w, "  %-16s %-24s %s\n", r.Backend, label, reason) //nolint:errcheck
		}
	}

	// Print detailed fix sections for NeedsSetup entries.
	for _, r := range needsSetup {
		printFixSection(w, r)
	}

	// Print detailed sections for Unavailable entries too — so users who
	// resolve a permanent blocker can see what else needs setup.
	for _, r := range unavailable {
		if r.InitErr != nil {
			continue // no cap results to show
		}
		hasFix := false
		for _, result := range r.Results {
			if result.Err != nil {
				hasFix = true
				break
			}
		}
		if hasFix {
			printFixSection(w, r)
		}
	}

	// Distro note.
	fmt.Fprintln(w, "\nNote: example commands assume Debian/Ubuntu. Adapt as needed for your distro.") //nolint:errcheck
}

// modeLabel returns a human-readable mode label for the report.
func modeLabel(r BackendReport) string {
	if r.IsBaseMode {
		return r.Mode + " (default)"
	}
	return r.Mode
}

// unavailableReason returns a short reason string for an unavailable backend.
func unavailableReason(r BackendReport) string {
	if r.InitErr != nil {
		// Trim to first line for the summary table.
		msg := r.InitErr.Error()
		if idx := strings.Index(msg, "\n"); idx >= 0 {
			msg = msg[:idx]
		}
		return msg
	}
	// Find first permanent failure.
	for _, result := range r.Results {
		if result.Err != nil && result.IsPermanent {
			return result.Err.Error()
		}
	}
	return "unavailable"
}

// countFailing counts checks that failed.
func countFailing(results []CheckResult) int {
	n := 0
	for _, r := range results {
		if r.Err != nil {
			n++
		}
	}
	return n
}

// printFixSection prints the detailed check list and fix steps for one BackendReport.
func printFixSection(w io.Writer, r BackendReport) {
	fmt.Fprintf(w, "\n%s\n", strings.Repeat("─", 52))             //nolint:errcheck
	fmt.Fprintf(w, "Needs setup: %s / %s\n\n", r.Backend, r.Mode) //nolint:errcheck

	for _, result := range r.Results {
		if result.Err == nil {
			fmt.Fprintf(w, "  ✓  %s\n", result.Cap.Summary) //nolint:errcheck
		} else {
			fmt.Fprintf(w, "  ✗  %-28s %s\n", result.Cap.Summary, result.Err.Error()) //nolint:errcheck
		}
	}

	// Print fix steps for each failed check.
	for _, result := range r.Results {
		if result.Err == nil {
			continue
		}
		if result.IsPermanent {
			fmt.Fprintf(w, "\n%s [permanent]\n", result.Cap.Summary) //nolint:errcheck
			if result.Cap.Detail != "" {
				fmt.Fprintf(w, "  %s\n", result.Cap.Detail) //nolint:errcheck
			}
			continue
		}
		if len(result.Steps) == 0 {
			continue
		}
		if len(result.Steps) == 1 {
			step := result.Steps[0]
			fmt.Fprintf(w, "\nTo fix %s:\n", result.Cap.Summary) //nolint:errcheck
			printStep(w, step, "  ")
		} else {
			fmt.Fprintf(w, "\nTo fix %s — choose one option:\n", result.Cap.Summary) //nolint:errcheck
			for i, step := range result.Steps {
				fmt.Fprintf(w, "  Option %s — %s:\n", optionLabel(i), step.Description) //nolint:errcheck
				printStep(w, step, "    ")
			}
		}
	}
}

// printStep prints a single FixStep.
func printStep(w io.Writer, step FixStep, indent string) {
	if step.NeedsRoot {
		prefix := indent + "(requires root)  "
		if step.Command != "" {
			for _, line := range strings.Split(step.Command, "\n") {
				fmt.Fprintf(w, "%s%s\n", prefix, line) //nolint:errcheck
				prefix = indent + "                 "
			}
		}
	} else if step.Command != "" {
		for _, line := range strings.Split(step.Command, "\n") {
			fmt.Fprintf(w, "%s%s\n", indent, line) //nolint:errcheck
		}
	}
	if step.URL != "" {
		fmt.Fprintf(w, "%sSee: %s\n", indent, step.URL) //nolint:errcheck
	}
}

// optionLabel returns "A", "B", "C", ... for step index i.
func optionLabel(i int) string {
	if i >= 0 && i < 26 {
		return string([]byte{byte('A') + byte(i)}) //nolint:gosec // G115: i is bounded 0-25
	}
	return fmt.Sprintf("%d", i+1)
}
