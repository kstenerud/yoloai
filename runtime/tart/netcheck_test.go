// ABOUTME: Tests for the pure network-liveness classifier and the fake-tart
// ABOUTME: exercised NetLiveness flow — the testable core of the doctor
// ABOUTME: vmnet-wedge detector.

package tart

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBridge builds a bridgeSubnet for tests. The IP is stored unmasked (the
// interface's own address, as net.Interfaces()/Addrs() would report it) so
// its String() form matches what a real bridge100 shows, e.g.
// "192.168.139.3/23" — mirroring the live repro in the task.
func testBridge(name, addr string, ones int) bridgeSubnet {
	return bridgeSubnet{name: name, net: &net.IPNet{IP: net.ParseIP(addr).To4(), Mask: net.CIDRMask(ones, 32)}}
}

func TestClassifyNetLiveness(t *testing.T) {
	tests := []struct {
		name       string
		ipOut      string
		ipErr      error
		en0Out     string
		execErr    error
		bridges    []bridgeSubnet
		wantState  runtime.NetHealthState
		wantDetail string
	}{
		{
			name:       "tart ip succeeds, address inside a host bridge subnet — ok",
			ipOut:      "192.168.64.12",
			ipErr:      nil,
			bridges:    []bridgeSubnet{testBridge("bridge100", "192.168.64.1", 24)},
			wantState:  runtime.NetHealthOK,
			wantDetail: "192.168.64.12",
		},
		{
			name:      "tart ip succeeds, address outside every host bridge subnet — stale DHCP lease, wedged",
			ipOut:     "192.168.65.2",
			ipErr:     nil,
			bridges:   []bridgeSubnet{testBridge("bridge100", "192.168.139.3", 23)},
			wantState: runtime.NetHealthWedged,
			wantDetail: "stale DHCP lease: guest has 192.168.65.2 but no host bridge is on that subnet " +
				"(bridge100 is 192.168.139.3/23)",
		},
		{
			name:       "tart ip succeeds, host has no bridge interfaces at all — can't judge, unknown",
			ipOut:      "192.168.64.12",
			ipErr:      nil,
			bridges:    nil,
			wantState:  runtime.NetHealthUnknown,
			wantDetail: "no host bridge interfaces found to verify 192.168.64.12 is routable",
		},
		{
			name:       "tart ip fails, guest en0 confirms link-local — wedged",
			ipOut:      "",
			ipErr:      errors.New("exit status 1: no IP address found"),
			en0Out:     "169.254.93.37",
			execErr:    nil,
			wantState:  runtime.NetHealthWedged,
			wantDetail: "guest en0 is link-local 169.254.93.37",
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
			wantDetail: "guest en0 is link-local 169.254.1.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, detail := classifyNetLiveness(tt.ipOut, tt.ipErr, tt.en0Out, tt.execErr, tt.bridges)
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
  {"Name": "yoloai-cli-embrace", "Running": true, "State": "running"},
  {"Name": "yoloai-cli-stopped", "Running": false, "State": "stopped"},
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
	rt := &Runtime{tartBin: fakeTart, execEnv: []string{"PATH=/usr/bin:/bin"}, layout: config.Layout{}.WithPrincipal(config.CLIPrincipal)}

	report, err := rt.NetLiveness(context.Background())
	require.NoError(t, err)
	require.Len(t, report.VMs, 1)

	vm := report.VMs[0]
	assert.Equal(t, "yoloai-cli-embrace", vm.VMName)
	assert.Equal(t, "embrace", vm.SandboxName)
	assert.Equal(t, runtime.NetHealthWedged, vm.State)
	assert.Equal(t, "guest en0 is link-local 169.254.93.37", vm.Detail)
}

// TestNetLiveness_HealthyVM_SkipsExecProbe verifies that when `tart ip`
// succeeds with an address inside a host bridge subnet, NetLiveness reports
// ok and never falls through to the guest exec probe. The fake script writes
// a marker file if `exec` is invoked; the test asserts it's absent. bridgeNets
// is injected (rather than relying on the real net.Interfaces() scan) so the
// test's classification doesn't depend on the host's actual bridge state.
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
	rt := &Runtime{
		tartBin: fakeTart, execEnv: []string{"PATH=/usr/bin:/bin"},
		bridgeNets: func() []bridgeSubnet { return []bridgeSubnet{testBridge("bridge100", "192.168.64.1", 24)} },
		layout:     config.Layout{}.WithPrincipal(config.CLIPrincipal),
	}

	report, err := rt.NetLiveness(context.Background())
	require.NoError(t, err)
	require.Len(t, report.VMs, 1)

	vm := report.VMs[0]
	assert.Equal(t, "yoloai-cli-embrace", vm.VMName)
	assert.Equal(t, "embrace", vm.SandboxName)
	assert.Equal(t, runtime.NetHealthOK, vm.State)
	assert.Equal(t, "192.168.64.12", vm.Detail)

	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr), "exec probe must not run when tart ip succeeds")
}

// TestNetLiveness_StaleLeaseVM reproduces the stale-DHCP-lease wedge variant
// (verified live on this host, see netcheck.go): `tart ip` succeeds with a
// non-empty address, but that address is outside every host bridge subnet.
// This must classify as wedged WITHOUT falling through to the guest exec
// probe — the host-side bridge check alone is conclusive. The fake script
// writes a marker file if `exec` is invoked; the test asserts it's absent.
func TestNetLiveness_StaleLeaseVM(t *testing.T) {
	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "exec-was-called")
	fakeTart := filepath.Join(tmpDir, "tart")
	script := `#!/bin/sh
case "$1" in
  list) echo '` + netLivenessListJSON + `' ;;
  ip) echo "192.168.65.2"; exit 0 ;;
  exec) touch "` + marker + `"; exit 99 ;;
  *) exit 99 ;;
esac
`
	require.NoError(t, os.WriteFile(fakeTart, []byte(script), 0700)) //nolint:gosec // G306: test binary needs execute bit
	rt := &Runtime{
		tartBin: fakeTart, execEnv: []string{"PATH=/usr/bin:/bin"},
		bridgeNets: func() []bridgeSubnet { return []bridgeSubnet{testBridge("bridge100", "192.168.139.3", 23)} },
		layout:     config.Layout{}.WithPrincipal(config.CLIPrincipal),
	}

	report, err := rt.NetLiveness(context.Background())
	require.NoError(t, err)
	require.Len(t, report.VMs, 1)

	vm := report.VMs[0]
	assert.Equal(t, "yoloai-cli-embrace", vm.VMName)
	assert.Equal(t, "embrace", vm.SandboxName)
	assert.Equal(t, runtime.NetHealthWedged, vm.State)
	assert.Equal(t,
		"stale DHCP lease: guest has 192.168.65.2 but no host bridge is on that subnet (bridge100 is 192.168.139.3/23)",
		vm.Detail)

	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr), "exec probe must not run when tart ip succeeds — the bridge check is host-side/in-process")
}

// TestSandboxNetHealth_WedgedVM exercises the single-sandbox probe: the
// sandbox name must map to the prefixed VM name, and the wedge signature
// (`tart ip` fails, guest en0 reports link-local) must classify as wedged.
// No `tart list` is involved — the caller guarantees the VM is running.
func TestSandboxNetHealth_WedgedVM(t *testing.T) {
	// Both subcommands guard on $2 so a wrong VM-name mapping classifies as
	// unknown (not wedged) and fails the assertions below.
	script := `#!/bin/sh
[ "$2" = "yoloai-cli-embrace" ] || { echo "wrong vm: $2" 1>&2; exit 98; }
case "$1" in
  ip) echo "no IP address found" 1>&2; exit 1 ;;
  exec) echo "169.254.93.37" ;;
  *) exit 99 ;;
esac
`
	fakeTart := writeFakeTart(t, script)
	rt := &Runtime{tartBin: fakeTart, execEnv: []string{"PATH=/usr/bin:/bin"}, layout: config.Layout{}.WithPrincipal(config.CLIPrincipal)}

	vm, err := rt.SandboxNetHealth(context.Background(), "embrace")
	require.NoError(t, err)
	assert.Equal(t, "yoloai-cli-embrace", vm.VMName)
	assert.Equal(t, "embrace", vm.SandboxName)
	assert.Equal(t, runtime.NetHealthWedged, vm.State)
	assert.Equal(t, "guest en0 is link-local 169.254.93.37", vm.Detail)
}
