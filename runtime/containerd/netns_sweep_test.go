// ABOUTME: Unit tests for selectOrphanNetns — the DF72 decision of which leaked
// ABOUTME: /var/run/netns entries a prune should reap, without touching real netns.
//go:build linux

package containerdrt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectOrphanNetns_DefaultPrincipal(t *testing.T) {
	// Sandbox "x" is running (container yoloai-x exists); "kreach" leaked (netns
	// present, no container record); "known-stopped" is in the known set.
	names := []string{
		"yoloai-yoloai-x",      // owned by a live container → keep
		"yoloai-yoloai-kreach", // orphan → reap
		"yoloai-yoloai-known",  // in known set → keep
		"cni-1a2b3c",           // not a yoloai netns → ignore
		"yoloai-notaninstance", // strips to "notaninstance", lacks instance prefix → ignore
	}
	existing := map[string]bool{"yoloai-x": true}
	known := map[string]bool{"yoloai-known": true}

	orphans := selectOrphanNetns(names, known, existing, "yoloai-")

	assert.Equal(t, []string{"yoloai-yoloai-kreach"}, orphans)
}

func TestSelectOrphanNetns_PrincipalScoped(t *testing.T) {
	// With principal "alice", instance prefix is "yoloai-alice-". A default
	// principal's orphan netns must NOT be reaped under alice's scope.
	names := []string{
		"yoloai-yoloai-alice-job", // alice's orphan → reap
		"yoloai-yoloai-bob-job",   // another principal → out of scope
		"yoloai-yoloai-job",       // default principal → out of scope
	}
	orphans := selectOrphanNetns(names, nil, nil, "yoloai-alice-")

	assert.Equal(t, []string{"yoloai-yoloai-alice-job"}, orphans)
}

func TestSelectOrphanNetns_NoneWhenAllOwned(t *testing.T) {
	names := []string{"yoloai-yoloai-a", "yoloai-yoloai-b"}
	existing := map[string]bool{"yoloai-a": true, "yoloai-b": true}

	assert.Empty(t, selectOrphanNetns(names, nil, existing, "yoloai-"))
}
