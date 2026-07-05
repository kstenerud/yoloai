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
func TestSidecarLabels_MakesLeakOrphanReapable(t *testing.T) {
	// Default principal: LabelSandbox present, LabelPrincipal omitted.
	labels := sidecarLabels("yoloai-box-netns-sidecar", "")
	assert.Equal(t, "yoloai-box-netns-sidecar", labels[runtime.LabelSandbox])
	_, hasPrincipal := labels[runtime.LabelPrincipal]
	assert.False(t, hasPrincipal, "default principal must not add the principal label")
	assert.True(t, runtime.IsOrphanCandidate(labels, ""), "leaked sidecar must be an orphan candidate")

	// Non-default principal: label present and scoping honored.
	scoped := sidecarLabels("yoloai-box-netns-sidecar", config.PrincipalSegment("alice"))
	assert.Equal(t, "alice", scoped[runtime.LabelPrincipal])
	assert.True(t, runtime.IsOrphanCandidate(scoped, "alice"))
	assert.False(t, runtime.IsOrphanCandidate(scoped, ""), "a different principal must not reap it")
}
