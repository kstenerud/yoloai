// ABOUTME: IsOrphanCandidate's principal-scoping rules for prune eligibility —
// ABOUTME: a container is only a candidate if its principal label matches the
// ABOUTME: caller's (DF19), never cross-principal or non-yoloai containers.
package runtime

import (
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
)

func TestIsOrphanCandidate(t *testing.T) {
	cases := []struct {
		name      string
		labels    map[string]string
		principal config.PrincipalSegment
		want      bool
	}{
		{
			name:      "matching principal",
			labels:    map[string]string{LabelSandbox: "mybox", LabelPrincipal: "alice"},
			principal: "alice",
			want:      true,
		},
		{
			name:      "different principal is excluded (DF19 scoping)",
			labels:    map[string]string{LabelSandbox: "mybox", LabelPrincipal: "alice"},
			principal: "bob",
			want:      false,
		},
		{
			// The match is EQUALITY, not prefix containment: "alice" must not reap
			// "alicia"'s instances even though one name prefixes the other. Nothing
			// upstream can currently construct this — InstancePrefix appends a "-"
			// delimiter and principals are alphanumeric, so namespaces cannot nest —
			// which is exactly why the predicate has to say so itself (DF115).
			name:      "a principal whose name prefixes another's is excluded",
			labels:    map[string]string{LabelSandbox: "mybox", LabelPrincipal: "alicia"},
			principal: "alice",
			want:      false,
		},
		{
			// A pre-D126 instance carries an empty principal label — what the CLI
			// stamped before it adopted "cli" — and it is the CLI's to reap (DF125).
			// The migration cannot have renamed it: it only walks sandboxes that
			// still have a sandbox dir, and an orphan by definition has none. Left
			// unclaimed, the debris is unreachable by any yoloai command forever.
			name:      "pre-D126 CLI instance is the CLI's to reap (DF125)",
			labels:    map[string]string{LabelSandbox: "mybox"},
			principal: config.CLIPrincipal,
			want:      true,
		},
		{
			// Docker stores `--label com.yoloai.principal=` as present-but-empty,
			// so the absent and empty forms must read identically.
			name:      "pre-D126 CLI instance, principal label present but empty",
			labels:    map[string]string{LabelSandbox: "mybox", LabelPrincipal: ""},
			principal: config.CLIPrincipal,
			want:      true,
		},
		{
			// The safety gate on the clause above, and the reason it is not simply
			// "an empty label matches anyone": the unprincipaled namespace was the
			// CLI's alone. An integrator adopting it is DF115 rebuilt by hand — the
			// CLI's sweep reaping every other principal's instances, in reverse.
			name:      "an integrator must NOT adopt the pre-D126 namespace (DF115)",
			labels:    map[string]string{LabelSandbox: "mybox", LabelPrincipal: ""},
			principal: "alice",
			want:      false,
		},
		{
			name:      "non-yoloai container (no sandbox label) is left alone",
			labels:    map[string]string{"com.example.thing": "x"},
			principal: config.CLIPrincipal,
			want:      false,
		},
		{
			// The live win over name-prefix matching: a hand-run container sitting in
			// yoloai's namespace by name only. Prefix matching destroyed it.
			name:      "container merely named yoloai-* but unlabeled is left alone",
			labels:    nil,
			principal: config.CLIPrincipal,
			want:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOrphanCandidate(tc.labels, tc.principal); got != tc.want {
				t.Errorf("IsOrphanCandidate(%v, %q) = %v, want %v", tc.labels, tc.principal, got, tc.want)
			}
		})
	}
}
