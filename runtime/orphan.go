// ABOUTME: Label-based identification of yoloai-managed instances for prune.
// ABOUTME: The canonical identity is the com.yoloai.* label set (D62), not the
// ABOUTME: yoloai-* name prefix — so prune reclaims exactly what yoloai created
// ABOUTME: and never a foreign container that merely happens to be named yoloai-*.
package runtime

import "github.com/kstenerud/yoloai/internal/config"

// IsOrphanCandidate reports whether a backend container carrying these labels is
// a yoloai-created instance owned by `principal` — i.e. a candidate for orphan
// pruning, subject to the caller's separate known-instances check. It returns:
//
//   - false when the container is not yoloai-created (no com.yoloai.sandbox
//     label) — so a container merely *named* yoloai-* (e.g. a hand-run
//     `docker run --name yoloai-x`) is left alone, where the old name-prefix
//     match would have removed it; and
//   - false when the container belongs to a DIFFERENT principal — the
//     com.yoloai.principal label is matched against `principal` by EQUALITY,
//     preserving the per-principal scoping the name prefix used to provide
//     (DF19). Equality, not prefix containment: `yoloai-` is a prefix of every
//     other principal's namespace, so a prefix match is not principal-disjoint
//     (DF115).
//
// The one exception is the CLI's own past: an instance created before D126
// carries an EMPTY principal label, because that is what the CLI stamped when it
// still elided its segment. Those instances are the CLI's, so the CLI's sweep
// still claims them — see legacyCLIInstance below and DF125.
//
// Every container yoloai creates is stamped with both labels at create time (see
// instanceLabels in the launch path), so this matches the exact set the yoloai-*
// name prefix did for real instances — only more precisely: the prefix also
// matched containers yoloai never created, which this does not.
func IsOrphanCandidate(labels map[string]string, principal config.PrincipalSegment) bool {
	if labels[LabelSandbox] == "" {
		return false
	}
	if labels[LabelPrincipal] == string(principal) {
		return true
	}
	return legacyCLIInstance(labels, principal)
}

// legacyCLIInstance reports whether labels identify an instance created by a
// pre-D126 CLI — yoloai-created (LabelSandbox set) with an EMPTY principal
// label, the value the CLI stamped before it adopted "cli" (D126).
//
// Why the CLI's sweep must claim these (DF125). The v4->v5 migration renames the
// instance of every sandbox that still has a sandbox dir; an instance whose dir
// is already gone (crash debris, or DF113's destroy-frees-the-name path) is
// never renamed and keeps its `yoloai-<name>` identity. Without this clause the
// CLI's sweep would filter it out forever ("" != "cli"), leaving debris no
// yoloai command can name — holding disk, and on tart a capped VM slot.
//
// This is NOT a dated compatibility shim, and needs no sunset: the label is a
// fact the backend recorded, not a guess parsed out of a name, so reading it
// stays exactly correct while the value simply stops occurring. It is D62's own
// rule — "runtime enumeration filters by label, not by name-string splitting" —
// applied to the identity the CLI used to have.
//
// Gated on the CLI's principal, which is the whole safety argument: the
// unprincipaled namespace was the CLI's alone, so letting any other principal's
// sweep adopt it would rebuild DF115 by hand. An integrator's sweep never
// matches an empty label.
//
// Not recoverable from the name, which is why this reads the label: a
// SandboxName may contain '-' where a PrincipalSegment may not, so the legacy
// form `yoloai-<S>` overlaps every principal namespace — `yoloai-acme-probe` is
// both the legacy instance of a sandbox named "acme-probe" and principal "acme"'s
// sandbox "probe" (DF125, verified against the parsers).
func legacyCLIInstance(labels map[string]string, principal config.PrincipalSegment) bool {
	return principal == config.CLIPrincipal && labels[LabelPrincipal] == ""
}
