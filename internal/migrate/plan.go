// ABOUTME: The plan/apply contract — a Migrator describes its operations
// ABOUTME: (each with the approval it needs) before the framework applies them.
package migrate

import "context"

// Auth is the approval an operation needs before it may run. It is the single
// axis the confirmation gate reasons over, so policy stays uniform across
// migrators instead of each hand-rolling its own prompts.
type Auth int

const (
	// AuthNone — benign, non-destructive; runs without confirmation.
	AuthNone Auth = iota
	// AuthConfirm — destructive; needs an explicit --yes (or an interactive
	// confirmation the app obtains). E.g. quarantining a sandbox that can't
	// migrate: its data is preserved, but the run is no longer a pure no-op.
	AuthConfirm
	// AuthAbandonOverlay — destructive AND discards uncommitted work; needs
	// --yes AND the explicit --abandon-stopped-overlay. A plain --yes can never
	// satisfy it, so --yes alone never destroys work. E.g. flattening a stopped
	// overlay sandbox whose upper changes are abandoned.
	AuthAbandonOverlay
)

// Op is one operation a migrator would perform, as surfaced in its plan.
type Op struct {
	// Description is a one-line, user-facing summary.
	Description string
	// Auth is the approval this op requires. Anything above AuthNone is
	// destructive and gates the whole run.
	Auth Auth
	// Sandbox, when set, is the sandbox the op concerns (for per-sandbox passes).
	Sandbox string
}

// Destructive reports whether the op needs any approval (i.e. mutates or
// discards user data).
func (o Op) Destructive() bool { return o.Auth > AuthNone }

// Plan is one migrator's read-only description of what it would do.
type Plan struct {
	// Migrator identifies the migrator (e.g. "v3->v4 overlay flatten").
	Migrator string
	// Ops are the operations it would perform, in order. Non-destructive and
	// destructive ops (including foreseen quarantines) are all listed here.
	Ops []Op
}

// Decision is the approval the app grants a run, derived from flags (and any
// interactive confirmation the app itself obtained). The library never prompts;
// it consumes a Decision and enforces it.
type Decision struct {
	// Yes authorizes benign destructive ops (AuthConfirm) — the scripted path,
	// or a granted interactive confirmation.
	Yes bool
	// AbandonStoppedOverlay additionally authorizes AuthAbandonOverlay ops.
	AbandonStoppedOverlay bool
}

// satisfies reports whether this decision meets an op's required approval.
func (d Decision) satisfies(a Auth) bool {
	switch a {
	case AuthNone:
		return true
	case AuthConfirm:
		return d.Yes
	case AuthAbandonOverlay:
		return d.Yes && d.AbandonStoppedOverlay
	default:
		return false
	}
}

// Report is a migrator's user-facing outcome, aggregated across a run for the
// app to render.
type Report struct {
	// Migrated names units brought to the current version.
	Migrated []string
	// Quarantined names units set aside (their data preserved) rather than
	// migrated.
	Quarantined []string
	// Notes are free-form user-facing lines (e.g. "stopped sandbox X to finalize
	// overlay->copy; restart to resume in copy mode").
	Notes []string
}

// Merge folds other into r.
func (r *Report) Merge(other Report) {
	r.Migrated = append(r.Migrated, other.Migrated...)
	r.Quarantined = append(r.Quarantined, other.Quarantined...)
	r.Notes = append(r.Notes, other.Notes...)
}

// Migrator is one discrete, individually-versioned migration. It is bespoke in
// what it does but never prompts or aborts interactively: it describes its work
// (Plan) and performs it (Apply), and the app owns all interaction between the
// two. Cross-migrator dependencies are not uniform, which is fine.
type Migrator interface {
	// Describe returns the migrator's identity for plan headers and logs.
	Describe() string
	// Plan inspects on-disk state READ-ONLY and returns the operations it would
	// perform. It resolves realm contents at their CURRENT physical location so
	// the plan is accurate even before a pending relocation runs.
	Plan(ctx context.Context) (Plan, error)
	// Apply performs the transform, RE-VALIDATING its preconditions against the
	// (possibly drifted) current state — it does not execute the planned ops
	// blindly. A precondition that drifted since planning (e.g. a container that
	// stopped) becomes a refusal or a per-unit skip governed by the Decision,
	// never an unreviewed change. It returns a Report of what it did.
	Apply(ctx context.Context, d Decision) (Report, error)
}

// Authorize reports whether decision satisfies every op across plans, returning
// the ops whose required approval is unmet — the app surfaces these (or prompts
// on the confirm-level ones). It is a pure function: no prompting, no mutation.
func Authorize(plans []Plan, d Decision) (ok bool, unmet []Op) {
	for _, p := range plans {
		for _, op := range p.Ops {
			if !d.satisfies(op.Auth) {
				unmet = append(unmet, op)
			}
		}
	}
	return len(unmet) == 0, unmet
}

// HasDestructive reports whether any plan contains a destructive op.
func HasDestructive(plans []Plan) bool {
	for _, p := range plans {
		for _, op := range p.Ops {
			if op.Destructive() {
				return true
			}
		}
	}
	return false
}
