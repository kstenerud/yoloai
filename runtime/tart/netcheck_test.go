// ABOUTME: Tests for the pure network-liveness classifier and the fake-tart
// ABOUTME: exercised NetLiveness flow — the testable core of the doctor
// ABOUTME: vmnet-wedge detector.

package tart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyNetLiveness(t *testing.T) {
	tests := []struct {
		name       string
		ipOut      string
		ipErr      error
		en0Out     string
		execErr    error
		wantState  runtime.NetHealthState
		wantDetail string
	}{
		{
			name:       "tart ip succeeds with a real address",
			ipOut:      "192.168.64.12",
			ipErr:      nil,
			wantState:  runtime.NetHealthOK,
			wantDetail: "192.168.64.12",
		},
		{
			name:       "tart ip fails, guest en0 confirms link-local — wedged",
			ipOut:      "",
			ipErr:      errors.New("exit status 1: no IP address found"),
			en0Out:     "169.254.93.37",
			execErr:    nil,
			wantState:  runtime.NetHealthWedged,
			wantDetail: "169.254.93.37",
		},
		{
			name:       "tart ip fails, guest en0 reports a normal address — suspect, not confirmed",
			ipOut:      "",
			ipErr:      errors.New("exit status 1: no IP address found"),
			en0Out:     "192.168.64.12",
			execErr:    nil,
			wantState:  runtime.NetHealthUnknown,
			wantDetail: "tart ip failed but guest en0 reports 192.168.64.12",
		},
		{
			name:       "tart ip fails, exec returns empty output — unknown",
			ipOut:      "",
			ipErr:      errors.New("exit status 1: no IP address found"),
			en0Out:     "",
			execErr:    nil,
			wantState:  runtime.NetHealthUnknown,
			wantDetail: "guest network probe returned no address",
		},
		{
			name:       "tart ip fails, exec itself errors — unknown",
			ipOut:      "",
			ipErr:      errors.New("exit status 1: no IP address found"),
			en0Out:     "",
			execErr:    errors.New("exit status 1: command not found"),
			wantState:  runtime.NetHealthUnknown,
			wantDetail: "guest network probe failed: exit status 1: command not found",
		},
		{
			name:       "tart ip succeeds but with empty output — falls through to signal 2",
			ipOut:      "",
			ipErr:      nil,
			en0Out:     "169.254.1.2",
			execErr:    nil,
			wantState:  runtime.NetHealthWedged,
			wantDetail: "169.254.1.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, detail := classifyNetLiveness(tt.ipOut, tt.ipErr, tt.en0Out, tt.execErr)
			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantDetail, detail)
		})
	}
}

// writeFakeTart writes a fake `tart` shell script to a temp dir and returns
// its path, mirroring the stop_escalation_test.go fake-binary pattern.
func writeFakeTart(t *testing.T, script string) string {
	t.Helper()
	tmpDir := t.TempDir()
	fakeTart := filepath.Join(tmpDir, "tart")
	require.NoError(t, os.WriteFile(fakeTart, []byte(script), 0700)) //nolint:gosec // G306: test binary needs execute bit
	return fakeTart
}

const netLivenessListJSON = `[
  {"Name": "yoloai-embrace", "Running": true, "State": "running"},
  {"Name": "yoloai-stopped", "Running": false, "State": "stopped"},
  {"Name": "some-other-vm", "Running": true, "State": "running"}
]`

// TestNetLiveness_WedgedVM exercises the full NetLiveness flow against a fake
// tart binary reproducing the verified real-world wedge: `tart ip` fails with
// "no IP address found" on stderr, and `tart exec ... getifaddr en0` echoes a
// 169.254.x.x link-local address. Only the running, yoloai-prefixed VM must be
// probed — the stopped VM and the non-yoloai VM must not appear in the report.
func TestNetLiveness_WedgedVM(t *testing.T) {
	script := `#!/bin/sh
case "$1" in
  list) echo '` + netLivenessListJSON + `' ;;
  ip) echo "no IP address found" 1>&2; exit 1 ;;
  exec) echo "169.254.93.37" ;;
  *) exit 99 ;;
esac
`
	fakeTart := writeFakeTart(t, script)
	rt := &Runtime{tartBin: fakeTart, execEnv: []string{"PATH=/usr/bin:/bin"}}

	report, err := rt.NetLiveness(context.Background())
	require.NoError(t, err)
	require.Len(t, report.VMs, 1)

	vm := report.VMs[0]
	assert.Equal(t, "yoloai-embrace", vm.VMName)
	assert.Equal(t, "embrace", vm.SandboxName)
	assert.Equal(t, runtime.NetHealthWedged, vm.State)
	assert.Equal(t, "169.254.93.37", vm.Detail)
}

// TestNetLiveness_HealthyVM_SkipsExecProbe verifies that when `tart ip`
// succeeds, NetLiveness reports ok and never falls through to the guest exec
// probe. The fake script writes a marker file if `exec` is invoked; the test
// asserts it's absent.
func TestNetLiveness_HealthyVM_SkipsExecProbe(t *testing.T) {
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "exec-was-called")
	fakeTart := filepath.Join(tmpDir, "tart")
	script := `#!/bin/sh
case "$1" in
  list) echo '` + netLivenessListJSON + `' ;;
  ip) echo "192.168.64.12"; exit 0 ;;
  exec) touch "` + marker + `"; exit 99 ;;
  *) exit 99 ;;
esac
`
	require.NoError(t, os.WriteFile(fakeTart, []byte(script), 0700)) //nolint:gosec // G306: test binary needs execute bit
	rt := &Runtime{tartBin: fakeTart, execEnv: []string{"PATH=/usr/bin:/bin"}}

	report, err := rt.NetLiveness(context.Background())
	require.NoError(t, err)
	require.Len(t, report.VMs, 1)

	vm := report.VMs[0]
	assert.Equal(t, "yoloai-embrace", vm.VMName)
	assert.Equal(t, "embrace", vm.SandboxName)
	assert.Equal(t, runtime.NetHealthOK, vm.State)
	assert.Equal(t, "192.168.64.12", vm.Detail)

	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr), "exec probe must not run when tart ip succeeds")
}

// TestSandboxNetHealth_WedgedVM exercises the single-sandbox probe: the
// sandbox name must map to the prefixed VM name, and the wedge signature
// (`tart ip` fails, guest en0 reports link-local) must classify as wedged.
// No `tart list` is involved — the caller guarantees the VM is running.
func TestSandboxNetHealth_WedgedVM(t *testing.T) {
	// Both subcommands guard on $2 so a wrong VM-name mapping classifies as
	// unknown (not wedged) and fails the assertions below.
	script := `#!/bin/sh
[ "$2" = "yoloai-embrace" ] || { echo "wrong vm: $2" 1>&2; exit 98; }
case "$1" in
  ip) echo "no IP address found" 1>&2; exit 1 ;;
  exec) echo "169.254.93.37" ;;
  *) exit 99 ;;
esac
`
	fakeTart := writeFakeTart(t, script)
	rt := &Runtime{tartBin: fakeTart, execEnv: []string{"PATH=/usr/bin:/bin"}}

	vm, err := rt.SandboxNetHealth(context.Background(), "embrace")
	require.NoError(t, err)
	assert.Equal(t, "yoloai-embrace", vm.VMName)
	assert.Equal(t, "embrace", vm.SandboxName)
	assert.Equal(t, runtime.NetHealthWedged, vm.State)
	assert.Equal(t, "169.254.93.37", vm.Detail)
}
