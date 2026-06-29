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
//     com.yoloai.principal label is matched against `principal` (absent label ==
//     the default ""), preserving the per-principal scoping the name prefix used
//     to provide (DF19).
//
// Every container yoloai creates is stamped with these labels at create time
// (see instanceLabels in the launch path; LabelSandbox is always set, the
// principal label only for non-default principals), so this matches the exact
// set the yoloai-* name prefix did for real instances — only more precisely.
func IsOrphanCandidate(labels map[string]string, principal config.PrincipalSegment) bool {
	if labels[LabelSandbox] == "" {
		return false
	}
	return labels[LabelPrincipal] == string(principal)
}
