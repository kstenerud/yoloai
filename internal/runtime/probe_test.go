// ABOUTME: Tests for SelectBackend's isolation/OS routing — the consolidated
// ABOUTME: backend-routing logic shared by the CLI and library embedders (F21).

package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSelectBackend_MacRouting covers the macOS-native routing branch,
// which returns before any registry lookup and so is deterministic
// regardless of which backends are compiled in. This is the logic F21
// moved out of cli/cliutil.ResolveBackend so the library shares it.
func TestSelectBackend_MacRouting(t *testing.T) {
	cases := []struct {
		name      string
		isolation IsolationMode
		want      BackendType
	}{
		{"mac+vm routes to tart", IsolationModeVM, BackendTart},
		{"mac+default routes to seatbelt", IsolationModeDefault, BackendSeatbelt},
		{"mac+container routes to seatbelt", IsolationModeContainer, BackendSeatbelt},
		{"mac+container-enhanced routes to seatbelt", IsolationModeContainerEnhanced, BackendSeatbelt},
		{"mac+vm-enhanced routes to seatbelt (only vm picks tart)", IsolationModeVMEnhanced, BackendSeatbelt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warn := SelectBackend(context.Background(), "", tc.isolation, "mac", nil)
			assert.Equal(t, tc.want, got)
			assert.Empty(t, warn, "OS routing emits no warning")
		})
	}
}

// TestSelectBackend_VMFallsThroughWhenContainerdAbsent verifies that
// vm/vm-enhanced isolation falls through to container-slot selection when
// containerd isn't registered (the macOS-host case, and the case here
// where the runtime package test registers no backends). It must not
// hard-return containerd when containerd can't be instantiated.
func TestSelectBackend_VMFallsThroughWhenContainerdAbsent(t *testing.T) {
	if IsAvailable(BackendContainerd) {
		t.Skip("containerd is registered in this build; fall-through path not exercised")
	}
	// No container backends registered either → SelectContainerBackend
	// returns the preferred name (or BackendDocker) so the caller fails
	// downstream in New() with a backend-specific error.
	got, _ := SelectBackend(context.Background(), "", IsolationModeVM, "", nil)
	assert.NotEqual(t, BackendContainerd, got,
		"must not return containerd when it isn't available")
}

// TestSelectBackend_NoRoutingDelegatesToContainerSlot confirms that empty
// isolation + empty OS is equivalent to plain container-slot selection —
// the back-compat path the simple callers (build, library default) rely
// on.
func TestSelectBackend_NoRoutingDelegatesToContainerSlot(t *testing.T) {
	gotRouted, _ := SelectBackend(context.Background(), "", IsolationModeDefault, "", nil)
	gotDirect, _ := SelectContainerBackend(context.Background(), "", nil)
	assert.Equal(t, gotDirect, gotRouted,
		"empty isolation/OS must match SelectContainerBackend exactly")
}

// TestProbeStatus_Ordering pins the tier order auto-pick relies on: a backend
// that is merely installed still outranks an absent one, and only a running
// backend clears the running threshold.
func TestProbeStatus_Ordering(t *testing.T) {
	assert.Less(t, ProbeAbsent, ProbeInstalled)
	assert.Less(t, ProbeInstalled, ProbeRunning)
	assert.GreaterOrEqual(t, ProbeRunning, ProbeInstalled, "running satisfies the installed threshold")
	assert.Equal(t, "absent", ProbeAbsent.String())
	assert.Equal(t, "installed", ProbeInstalled.String())
	assert.Equal(t, "running", ProbeRunning.String())
}

// TestProbe_UnregisteredBackendIsAbsent verifies the helpers treat a backend
// that isn't registered on this platform as Absent — neither installed nor
// running — with a reason, so selection/diagnostics fail clearly.
func TestProbe_UnregisteredBackendIsAbsent(t *testing.T) {
	const bogus BackendType = "no-such-backend-xyz"
	status, reason := Probe(context.Background(), bogus, nil)
	assert.Equal(t, ProbeAbsent, status)
	assert.NotEmpty(t, reason)

	installed, _ := Installed(context.Background(), bogus, nil)
	assert.False(t, installed)
	running, _ := Running(context.Background(), bogus, nil)
	assert.False(t, running)
}
