// ABOUTME: Network-liveness probe for tart VMs — detects the vmnet session
// ABOUTME: wedge that leaves a running VM's guest network dead (link-local
// ABOUTME: en0) after host sleep. Pure classification core + thin CLI shell,
// ABOUTME: mirroring census.go. Report-only: never restarts anything.

package tart

import (
	"context"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
)

// Compile-time check: tart reports guest network liveness.
var _ runtime.NetLivenessReporter = (*Runtime)(nil)

// Compile-time check: tart can also probe one sandbox's guest network health
// on demand (ls/info), reusing the same probe NetLiveness runs fleet-wide.
var _ runtime.SandboxNetHealthProber = (*Runtime)(nil)

// en0GuestAddrCmd is the guest-side command that probes en0's address via
// `tart exec`. -- separators are unsupported by tart exec (see
// docs/contributors/backend-idiosyncrasies.md, "Tart exec does not support --
// argument separator"); execArgs already omits one.
const en0GuestAddrCmd = "/usr/sbin/ipconfig"

// NetLiveness implements runtime.NetLivenessReporter. It probes every running
// yoloai-owned VM for the vmnet wedge described in
// docs/contributors/backend-idiosyncrasies.md ("Tart: vmnet session wedges on
// a long-idle VM"): the guest's en0 drops to a 169.254.x.x link-local address
// while `tart exec` (whose control channel is Virtualization.framework, not
// IP) keeps working fine. Detection is probe-only — doctor reports a wedge,
// it never restarts a VM.
func (r *Runtime) NetLiveness(ctx context.Context) (runtime.NetLivenessReport, error) {
	entries, err := r.listEntries(ctx)
	if err != nil {
		return runtime.NetLivenessReport{}, err
	}
	var report runtime.NetLivenessReport
	for _, e := range entries {
		if !e.Running || !strings.HasPrefix(e.Name, instancePrefix) {
			continue
		}
		state, detail := r.probeNetLiveness(ctx, e.Name)
		report.VMs = append(report.VMs, runtime.VMNetHealth{
			SandboxName: sandboxName(e.Name),
			VMName:      e.Name,
			State:       state,
			Detail:      detail,
		})
	}
	return report, nil
}

// SandboxNetHealth implements runtime.SandboxNetHealthProber. Unlike
// NetLiveness, it does not call `tart list` first — the caller (the status
// read-model) only invokes this for a sandbox it has already confirmed is
// running, so the extra existence/running check would be redundant.
func (r *Runtime) SandboxNetHealth(ctx context.Context, name string) (runtime.VMNetHealth, error) {
	// Callers pass the sandbox name (e.g. "mybox"); the Tart VM is named with
	// the instance prefix (e.g. "yoloai-mybox"). Same idiom as GitExec.
	vmName := instancePrefix + strings.TrimPrefix(name, instancePrefix)
	state, detail := r.probeNetLiveness(ctx, vmName)
	return runtime.VMNetHealth{
		SandboxName: sandboxName(vmName),
		VMName:      vmName,
		State:       state,
		Detail:      detail,
	}, nil
}

// probeNetLiveness runs the two-signal liveness probe for one VM. Signal 2
// (`tart exec ... ipconfig getifaddr en0`) only runs when signal 1 (`tart ip`)
// failed or returned nothing — a healthy VM never pays the extra exec.
// classifyNetLiveness does the actual decision.
func (r *Runtime) probeNetLiveness(ctx context.Context, vmName string) (runtime.NetHealthState, string) {
	ipOut, ipErr := r.runTart(ctx, "ip", vmName)
	if ipErr == nil && strings.TrimSpace(ipOut) != "" {
		return classifyNetLiveness(ipOut, ipErr, "", nil)
	}
	en0Out, execErr := r.runTart(ctx, execArgs(vmName, en0GuestAddrCmd, "getifaddr", "en0")...)
	return classifyNetLiveness(ipOut, ipErr, en0Out, execErr)
}

// classifyNetLiveness is the pure decision core for the two-signal probe (see
// docs/contributors/backend-idiosyncrasies.md, "Tart: vmnet session wedges on
// a long-idle VM"). A guest en0 address in the 169.254.0.0/16 link-local range
// confirms the vmnet session is wedged. Any other outcome — including a
// normal guest address returned while `tart ip` still failed — is reported as
// unknown/suspect rather than confirmed wedged, since that combination could
// equally mean the guest is still mid-boot with DHCP pending.
func classifyNetLiveness(ipOut string, ipErr error, en0Out string, execErr error) (runtime.NetHealthState, string) {
	if ip := strings.TrimSpace(ipOut); ipErr == nil && ip != "" {
		return runtime.NetHealthOK, ip
	}
	en0 := strings.TrimSpace(en0Out)
	switch {
	case execErr != nil:
		return runtime.NetHealthUnknown, "guest network probe failed: " + execErr.Error()
	case en0 == "":
		return runtime.NetHealthUnknown, "guest network probe returned no address"
	case strings.HasPrefix(en0, "169.254."):
		return runtime.NetHealthWedged, en0
	default:
		return runtime.NetHealthUnknown, "tart ip failed but guest en0 reports " + en0
	}
}
