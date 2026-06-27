package caps

// ABOUTME: Core types for the capability registry: HostCapability, FixStep, Availability,
// ABOUTME: CheckResult, BackendReport, and Environment.

import (
	"context"
	"io"
)

// HostCapability describes one system prerequisite, how to test for it,
// whether a failure is permanent, and how to help the user fix it.
type HostCapability struct {
	ID      string // stable machine-readable key, e.g. "kvm-device"
	Summary string // short label, e.g. "KVM device access"
	Detail  string // why it's needed; shown in system doctor output

	// Check returns nil if the capability is satisfied.
	Check func(ctx context.Context) error

	// Permanent returns true when a failed check cannot be resolved within
	// the current environment — e.g. no KVM hardware, wrong OS. Called only
	// when Check returns a non-nil error. If nil, failures are assumed fixable.
	Permanent func(env Environment) bool

	// Fix returns ordered remediation steps tailored to the host environment.
	// Called only when Check fails and Permanent returns false (or is nil).
	// May return nil when no command-level guidance is available.
	Fix func(env Environment) []FixStep
}

// FixStep is one discrete remediation action.
type FixStep struct {
	Description string // human explanation
	Command     string // example shell command; empty if no command applies.
	// Commands are illustrative (typically Debian/Ubuntu);
	// the user is expected to adapt them to their distro.
	URL       string // reference URL for documentation; empty if not applicable
	NeedsRoot bool   // true when the command typically requires sudo or root;
	// used for display labelling only — never gates execution.
}

// Availability classifies a (backend, mode) combination after all checks run.
type Availability int

const (
	Ready       Availability = iota // all checks passed
	NeedsSetup                      // all failures are fixable
	Unavailable                     // at least one failure is permanent
)

// CheckResult records the outcome of one capability check.
type CheckResult struct {
	Cap         HostCapability
	Err         error     // nil = satisfied
	IsPermanent bool      // true when Err != nil and Cap.Permanent(env) == true
	Steps       []FixStep // populated only when Err != nil and not permanent
}

// BackendReport holds the full result for one (backend, mode) combination.
// It is the unit passed to FormatDoctor.
type BackendReport struct {
	Backend      string        // e.g. "docker", "containerd"
	Mode         string        // isolation mode label, or BaseModeName() for the base check
	IsBaseMode   bool          // true when Mode is the no-isolation base mode
	InitErr      error         // non-nil if backend New() failed; Results will be nil
	Results      []CheckResult // nil when InitErr != nil
	Availability Availability  // computed from Results; Unavailable when InitErr != nil
}

// Environment holds host context used by Permanent and Fix functions.
// Detected once per invocation; passed to all capability calls.
type Environment struct {
	IsRoot      bool // os.Getuid() == 0
	IsWSL2      bool // /proc/version contains "microsoft"
	InContainer bool // /.dockerenv exists, or cgroup shows container runtime
	KVMGroup    bool // current user is a member of the "kvm" group
}

// Writer is an alias for io.Writer to avoid importing io in caps users.
type Writer = io.Writer
