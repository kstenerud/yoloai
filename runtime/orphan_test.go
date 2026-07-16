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
			// A pre-D126 instance carries no principal label, so it belongs to no
			// principal and no principal reaps it. `yoloai system migrate` recreates
			// it under its owner rather than leaving a sweep to guess.
			name:      "pre-D126 instance (no principal label) belongs to nobody",
			labels:    map[string]string{LabelSandbox: "mybox"},
			principal: config.CLIPrincipal,
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
