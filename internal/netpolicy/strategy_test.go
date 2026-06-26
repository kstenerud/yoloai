// ABOUTME: strategy_test.go tests CanEnforce: the capability model that decides
// ABOUTME: whether a (backend, isolation-mode) pair can enforce a network allowlist.

package netpolicy_test

import (
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/netpolicy"
	"github.com/kstenerud/yoloai/internal/runtime"
)

func TestCanEnforce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		strategy    netpolicy.Strategy
		caps        runtime.BackendCaps
		backend     runtime.BackendType
		isolation   runtime.IsolationMode
		wantOK      bool
		wantContain string // substring that must appear in reason when !ok
	}{
		{
			name:        "IPFilter backend does not support network isolation",
			strategy:    netpolicy.StrategyIPFilter,
			caps:        runtime.BackendCaps{NetworkIsolation: false},
			backend:     runtime.BackendDocker,
			isolation:   runtime.IsolationModeContainer,
			wantOK:      false,
			wantContain: "not supported by",
		},
		{
			name:        "IPFilter backend name appears in not-supported message",
			strategy:    netpolicy.StrategyIPFilter,
			caps:        runtime.BackendCaps{NetworkIsolation: false},
			backend:     runtime.BackendDocker,
			isolation:   runtime.IsolationModeContainer,
			wantOK:      false,
			wantContain: string(runtime.BackendDocker),
		},
		{
			name:        "IPFilter gVisor ignores in-sandbox iptables",
			strategy:    netpolicy.StrategyIPFilter,
			caps:        runtime.BackendCaps{NetworkIsolation: true},
			backend:     runtime.BackendDocker,
			isolation:   runtime.IsolationModeContainerEnhanced,
			wantOK:      false,
			wantContain: "gVisor",
		},
		{
			name:      "IPFilter container mode succeeds",
			strategy:  netpolicy.StrategyIPFilter,
			caps:      runtime.BackendCaps{NetworkIsolation: true},
			backend:   runtime.BackendDocker,
			isolation: runtime.IsolationModeContainer,
			wantOK:    true,
		},
		{
			name:      "IPFilter default (empty) mode succeeds",
			strategy:  netpolicy.StrategyIPFilter,
			caps:      runtime.BackendCaps{NetworkIsolation: true},
			backend:   runtime.BackendDocker,
			isolation: runtime.IsolationModeDefault,
			wantOK:    true,
		},
		{
			name:      "IPFilter VM mode succeeds",
			strategy:  netpolicy.StrategyIPFilter,
			caps:      runtime.BackendCaps{NetworkIsolation: true},
			backend:   runtime.BackendDocker,
			isolation: runtime.IsolationModeVM,
			wantOK:    true,
		},
		{
			name:        "unknown strategy is not implemented",
			strategy:    netpolicy.Strategy("bogus"),
			caps:        runtime.BackendCaps{NetworkIsolation: true},
			backend:     runtime.BackendDocker,
			isolation:   runtime.IsolationModeContainer,
			wantOK:      false,
			wantContain: "not implemented",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, reason := netpolicy.CanEnforce(tc.strategy, tc.caps, tc.backend, tc.isolation)
			if ok != tc.wantOK {
				t.Errorf("CanEnforce ok=%v, want %v (reason=%q)", ok, tc.wantOK, reason)
			}
			if ok {
				if reason != "" {
					t.Errorf("ok==true but reason=%q, want empty", reason)
				}
			} else {
				if tc.wantContain != "" && !strings.Contains(reason, tc.wantContain) {
					t.Errorf("reason=%q does not contain %q", reason, tc.wantContain)
				}
			}
		})
	}
}
