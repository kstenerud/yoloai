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
			name:      "default-principal instance (no principal label)",
			labels:    map[string]string{LabelSandbox: "mybox"},
			principal: "",
			want:      true,
		},
		{
			name:      "matching non-default principal",
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
			name:      "default principal must not match a principal-owned instance",
			labels:    map[string]string{LabelSandbox: "mybox", LabelPrincipal: "alice"},
			principal: "",
			want:      false,
		},
		{
			name:      "non-yoloai container (no sandbox label) is left alone",
			labels:    map[string]string{"com.example.thing": "x"},
			principal: "",
			want:      false,
		},
		{
			name:      "container merely named yoloai-* but unlabeled is left alone",
			labels:    nil,
			principal: "",
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
