// ABOUTME: Public read-model for the system doctor — hand-written mirrors of the
// ABOUTME: internal caps.BackendReport tree that decouple the public API from the
// ABOUTME: capability registry's internal types (which carry unexportable funcs).

package yoloai

import "github.com/kstenerud/yoloai/runtime/caps"

// Availability classifies a (backend, isolation-mode) combination after all of
// its host-capability checks have run.
type Availability int

const (
	// Ready means every required capability check passed.
	Ready Availability = iota
	// NeedsSetup means at least one check failed but all failures are fixable.
	NeedsSetup
	// Unavailable means at least one failure is permanent (hardware/OS mismatch).
	Unavailable
)

// BackendReport is one backend's diagnostic report from Doctor: its base-mode
// availability, or — for a non-base row — the per-isolation-mode capability
// check breakdown. It is a hand-written public mirror of the internal
// caps.BackendReport, so embedders can read doctor results without importing
// internal packages.
type BackendReport struct {
	// Type is the backend type, e.g. "docker", "containerd".
	Type BackendType
	// Mode is the isolation-mode label, or the base-mode name for the base row.
	Mode string
	// IsBaseMode is true when Mode is the no-isolation base mode.
	IsBaseMode bool
	// InitErr is non-nil when the backend could not be constructed; Results is
	// then nil and Availability is Unavailable.
	InitErr error
	// Results is the per-capability outcome list; nil when InitErr != nil.
	Results []CapabilityCheck
	// Availability is the aggregate computed from Results.
	Availability Availability
}

// CapabilityCheck records the outcome of one host-capability check.
type CapabilityCheck struct {
	// Capability identifies the prerequisite that was checked.
	Capability Capability
	// Err is nil when the capability is satisfied.
	Err error
	// IsPermanent is true when Err != nil and the failure cannot be fixed in
	// this environment.
	IsPermanent bool
	// Steps holds remediation guidance; populated only for fixable failures.
	Steps []FixStep
}

// Capability describes one host prerequisite. It mirrors the descriptive
// fields of the internal caps.HostCapability, dropping the unexportable
// Check/Permanent/Fix function fields (their results are surfaced via
// CapabilityCheck instead).
type Capability struct {
	// ID is a stable machine-readable key, e.g. "kvm-device".
	ID string
	// Summary is a short label, e.g. "KVM device access".
	Summary string
	// Detail explains why the capability is needed.
	Detail string
}

// FixStep is one discrete remediation action for a fixable capability failure.
type FixStep struct {
	// Description is a human explanation of the step.
	Description string
	// Command is an example shell command; empty when no command applies.
	// Commands are illustrative (typically Debian/Ubuntu).
	Command string
	// URL is a reference URL; empty when not applicable.
	URL string
	// NeedsRoot is true when the command typically requires sudo or root. Used
	// for display labelling only — it never gates execution.
	NeedsRoot bool
}

// backendReportFromCaps converts one internal caps.BackendReport into its
// public mirror.
func backendReportFromCaps(r caps.BackendReport) BackendReport {
	out := BackendReport{
		Type:         BackendType(r.Backend),
		Mode:         r.Mode,
		IsBaseMode:   r.IsBaseMode,
		InitErr:      r.InitErr,
		Availability: availabilityFromCaps(r.Availability),
	}
	for _, cr := range r.Results {
		out.Results = append(out.Results, capabilityCheckFromCaps(cr))
	}
	return out
}

func capabilityCheckFromCaps(cr caps.CheckResult) CapabilityCheck {
	out := CapabilityCheck{
		Capability: Capability{
			ID:      cr.Cap.ID,
			Summary: cr.Cap.Summary,
			Detail:  cr.Cap.Detail,
		},
		Err:         cr.Err,
		IsPermanent: cr.IsPermanent,
	}
	for _, s := range cr.Steps {
		out.Steps = append(out.Steps, FixStep{
			Description: s.Description,
			Command:     s.Command,
			URL:         s.URL,
			NeedsRoot:   s.NeedsRoot,
		})
	}
	return out
}

func availabilityFromCaps(a caps.Availability) Availability {
	switch a {
	case caps.Ready:
		return Ready
	case caps.NeedsSetup:
		return NeedsSetup
	default:
		return Unavailable
	}
}
