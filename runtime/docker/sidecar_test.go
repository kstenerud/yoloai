// ABOUTME: Unit tests for the netns sidecar's canonical-label stamping, which
// ABOUTME: keeps a crash-leaked sidecar reclaimable by the D62 orphan sweep.
package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// A leaked sidecar must satisfy IsOrphanCandidate so `yoloai system prune`
// reaps it. That needs LabelSandbox set; the principal label mirrors the
// launch path (present only for a non-default principal).
// TestSidecarLabels_MakesLeakOrphanReapable: a sidecar that crash-leaks carries
// enough identity for its OWN principal's sweep to reap it, and not enough for
// anyone else's. The default-principal case this used to cover is gone with D126
// — sidecarLabels("") cannot occur now that every principal is non-empty.
func TestSidecarLabels_MakesLeakOrphanReapable(t *testing.T) {
	const name = "yoloai-box-netns-sidecar"
	labels := sidecarLabels(name, config.CLIPrincipal)
	assert.Equal(t, name, labels[runtime.LabelSandbox])
	assert.Equal(t, string(config.CLIPrincipal), labels[runtime.LabelPrincipal])
	assert.True(t, runtime.IsOrphanCandidate(labels, config.CLIPrincipal), "leaked sidecar must be an orphan candidate")

	scoped := sidecarLabels(name, config.PrincipalSegment("alice"))
	assert.Equal(t, "alice", scoped[runtime.LabelPrincipal])
	assert.True(t, runtime.IsOrphanCandidate(scoped, "alice"))
	assert.False(t, runtime.IsOrphanCandidate(scoped, config.CLIPrincipal), "a different principal must not reap it")
}
