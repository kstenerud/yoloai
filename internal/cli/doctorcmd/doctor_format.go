package doctorcmd

// ABOUTME: Human-readable formatter for `yoloai doctor` capability reports — the
// ABOUTME: three-tier summary table plus per-failure fix detail. Consumes the
// ABOUTME: public yoloai.BackendReport read-model.

import (
	"fmt"
	"io"
	"strings"

	"github.com/kstenerud/yoloai"
)

// formatDoctor writes the full three-tier summary table followed by per-failure
// fix details to w. Takes a slice of BackendReport covering all (backend, mode)
// combinations, including base modes. Handles empty slices gracefully. For
// Unavailable entries, all failed checks are shown with fix steps where
// available — permanent failures are labelled as such.
func formatDoctor(w io.Writer, reports []yoloai.BackendReport) {
	if len(reports) == 0 {
		fmt.Fprintln(w, "No backends available to check.") //nolint:errcheck
		return
	}

	// Partition into three tiers.
	var ready, needsSetup, unavailable []yoloai.BackendReport
	for _, r := range reports {
		switch r.Availability {
		case yoloai.Ready:
			ready = append(ready, r)
		case yoloai.NeedsSetup:
			needsSetup = append(needsSetup, r)
		default:
			unavailable = append(unavailable, r)
		}
	}

	// Print summary table.
	if len(ready) > 0 {
		fmt.Fprintln(w, "\nReady to use:") //nolint:errcheck
		for _, r := range ready {
			fmt.Fprintf(w, "  %-16s %s\n", r.Type, modeLabel(r)) //nolint:errcheck
		}
	}

	if len(needsSetup) > 0 {
		fmt.Fprintln(w, "\nNeeds setup:") //nolint:errcheck
		for _, r := range needsSetup {
			fmt.Fprintf(w, "  %-16s %-24s %d of %d checks failing\n", r.Type, modeLabel(r), countFailing(r.Results), len(r.Results)) //nolint:errcheck
		}
	}

	if len(unavailable) > 0 {
		fmt.Fprintln(w, "\nNot available on this machine:") //nolint:errcheck
		for _, r := range unavailable {
			fmt.Fprintf(w, "  %-16s %-24s %s\n", r.Type, modeLabel(r), unavailableReason(r)) //nolint:errcheck
		}
	}

	// Print detailed fix sections for NeedsSetup entries.
	for _, r := range needsSetup {
		printFixSection(w, r)
	}

	// Print detailed sections for Unavailable entries too — so users who
	// resolve a permanent blocker can see what else needs setup.
	printUnavailableFixSections(w, unavailable)

	// Distro note.
	fmt.Fprintln(w, "\nNote: example commands assume Debian/Ubuntu. Adapt as needed for your distro.") //nolint:errcheck
}

// printUnavailableFixSections prints detailed fix sections for unavailable
// backends that have actionable results (i.e., non-init errors).
func printUnavailableFixSections(w io.Writer, unavailable []yoloai.BackendReport) {
	for _, r := range unavailable {
		if r.InitErr != nil {
			continue // no cap results to show
		}
		for _, result := range r.Results {
			if result.Err != nil {
				printFixSection(w, r)
				break
			}
		}
	}
}

// modeLabel returns a human-readable mode label for the report.
func modeLabel(r yoloai.BackendReport) string {
	if r.IsBaseMode {
		return r.Mode + " (default)"
	}
	return r.Mode
}

// unavailableReason returns a short reason string for an unavailable backend.
func unavailableReason(r yoloai.BackendReport) string {
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
func countFailing(results []yoloai.CapabilityCheck) int {
	n := 0
	for _, r := range results {
		if r.Err != nil {
			n++
		}
	}
	return n
}

// printFixSection prints the detailed check list and fix steps for one report.
func printFixSection(w io.Writer, r yoloai.BackendReport) {
	fmt.Fprintf(w, "\n%s\n", strings.Repeat("─", 52))          //nolint:errcheck
	fmt.Fprintf(w, "Needs setup: %s / %s\n\n", r.Type, r.Mode) //nolint:errcheck

	for _, result := range r.Results {
		if result.Err == nil {
			fmt.Fprintf(w, "  ✓  %s\n", result.Capability.Summary) //nolint:errcheck
		} else {
			fmt.Fprintf(w, "  ✗  %-28s %s\n", result.Capability.Summary, result.Err.Error()) //nolint:errcheck
		}
	}

	// Print fix steps for each failed check.
	for _, result := range r.Results {
		if result.Err != nil {
			printCheckFix(w, result)
		}
	}
}

// printCheckFix renders the fix guidance for one failed capability check: a
// permanence note, a single "To fix" block, or a multi-option list.
func printCheckFix(w io.Writer, result yoloai.CapabilityCheck) {
	if result.IsPermanent {
		fmt.Fprintf(w, "\n%s [permanent]\n", result.Capability.Summary) //nolint:errcheck
		if result.Capability.Detail != "" {
			fmt.Fprintf(w, "  %s\n", result.Capability.Detail) //nolint:errcheck
		}
		return
	}
	if len(result.Steps) == 0 {
		return
	}
	if len(result.Steps) == 1 {
		step := result.Steps[0]
		fmt.Fprintf(w, "\nTo fix %s:\n", result.Capability.Summary) //nolint:errcheck
		// Surface the description like the multi-step "Option A — <desc>" label
		// does; without it a description-only step (e.g. "KVM hardware not
		// detected …", which carries no command) prints an empty section.
		if step.Description != "" {
			fmt.Fprintf(w, "  %s\n", step.Description) //nolint:errcheck
		}
		printStep(w, step, "  ")
		return
	}
	fmt.Fprintf(w, "\nTo fix %s — choose one option:\n", result.Capability.Summary) //nolint:errcheck
	for i, step := range result.Steps {
		fmt.Fprintf(w, "  Option %s — %s:\n", optionLabel(i), step.Description) //nolint:errcheck
		printStep(w, step, "    ")
	}
}

// printStep prints a single FixStep.
func printStep(w io.Writer, step yoloai.FixStep, indent string) {
	if step.NeedsRoot {
		prefix := indent + "(requires root)  "
		if step.Command != "" {
			for line := range strings.SplitSeq(step.Command, "\n") {
				fmt.Fprintf(w, "%s%s\n", prefix, line) //nolint:errcheck
				prefix = indent + "                 "
			}
		}
	} else if step.Command != "" {
		for line := range strings.SplitSeq(step.Command, "\n") {
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
