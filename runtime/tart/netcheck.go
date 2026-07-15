// ABOUTME: Network-liveness probe for tart VMs — detects the vmnet session
// ABOUTME: wedge that leaves a running VM's guest network dead (link-local
// ABOUTME: en0) after host sleep. Pure classification core + thin CLI shell,
// ABOUTME: mirroring census.go. Report-only: never restarts anything.

package tart

import (
	"context"
	"fmt"
	"net"
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
// failed or returned nothing — a healthy VM never pays the extra exec. When
// signal 1 succeeds, the returned address is checked in-process against the
// host's bridge* subnets (see classifyGuestAddr) — no extra exec either way.
// classifyNetLiveness does the actual decision.
func (r *Runtime) probeNetLiveness(ctx context.Context, vmName string) (runtime.NetHealthState, string) {
	ipOut, ipErr := r.runTart(ctx, "ip", vmName)
	if ipErr == nil && strings.TrimSpace(ipOut) != "" {
		return classifyNetLiveness(ipOut, ipErr, "", nil, r.bridgeSubnets())
	}
	en0Out, execErr := r.runTart(ctx, execArgs(vmName, en0GuestAddrCmd, "getifaddr", "en0")...)
	return classifyNetLiveness(ipOut, ipErr, en0Out, execErr, nil)
}

// bridgeSubnets returns the host's bridge* interface subnets, using the
// injected r.bridgeNets seam when set (tests) or the real net.Interfaces()
// scan otherwise (production; wired by New()). Mirrors the r.hostMajor
// nil-fallback idiom in build.go.
func (r *Runtime) bridgeSubnets() []bridgeSubnet {
	if r.bridgeNets != nil {
		return r.bridgeNets()
	}
	return hostBridgeSubnets()
}

// bridgeSubnet is one host bridge* interface's IPv4 subnet — the vmnet
// gateway for NAT'd tart guests lives on one of these. name is the interface
// name (e.g. "bridge100"), used only for the human-readable detail string.
type bridgeSubnet struct {
	name string
	net  *net.IPNet
}

// hostBridgeSubnets gathers the host's bridge* interface IPv4 subnets natively
// (no guest exec, no `tart` subprocess) via net.Interfaces()/Addrs(). A NAT'd
// tart guest is only routable if its address falls inside one of these — the
// vmnet gateway is bound to a bridge* interface. Errors are swallowed (best
// effort): a failure here degrades classifyGuestAddr to "unknown", never to a
// false wedge report.
func hostBridgeSubnets() []bridgeSubnet {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []bridgeSubnet
	for _, iface := range ifaces {
		if !strings.HasPrefix(iface.Name, "bridge") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			out = append(out, bridgeSubnet{name: iface.Name, net: ipNet})
		}
	}
	return out
}

// classifyNetLiveness is the pure decision core for the two-signal probe (see
// docs/contributors/backend-idiosyncrasies.md, "Tart: vmnet session wedges on
// a long-idle VM"). There are two confirmed-wedge signatures:
//
//  1. `tart ip` fails and the guest's en0 has fallen back to a 169.254.0.0/16
//     link-local address — the classic long-idle wedge.
//  2. `tart ip` succeeds with a real-looking address, but that address is
//     outside every host bridge* subnet — a stale DHCP lease from a
//     superseded vmnet subnet. `tart ip` reads host-side lease records, not
//     guest liveness, so it happily returns an address the guest can no
//     longer route through (bridgeNets, classifyGuestAddr).
//
// Any other outcome — including a normal guest address returned while
// `tart ip` still failed — is reported as unknown/suspect rather than
// confirmed wedged, since that combination could equally mean the guest is
// still mid-boot with DHCP pending.
func classifyNetLiveness(ipOut string, ipErr error, en0Out string, execErr error, bridges []bridgeSubnet) (runtime.NetHealthState, string) {
	if ip := strings.TrimSpace(ipOut); ipErr == nil && ip != "" {
		return classifyGuestAddr(ip, bridges)
	}
	en0 := strings.TrimSpace(en0Out)
	switch {
	case execErr != nil:
		return runtime.NetHealthUnknown, "guest network probe failed: " + execErr.Error()
	case en0 == "":
		return runtime.NetHealthUnknown, "guest network probe returned no address"
	case strings.HasPrefix(en0, "169.254."):
		return runtime.NetHealthWedged, "guest en0 is link-local " + en0
	default:
		return runtime.NetHealthUnknown, "tart ip failed but guest en0 reports " + en0
	}
}

// classifyGuestAddr judges whether the address `tart ip` returned is actually
// routable: a NAT'd tart guest only works if its address falls inside a
// subnet a host bridge* interface is on (the vmnet gateway lives there). No
// bridge interfaces at all means the host can't be judged either way
// (unknown, not wedged) — e.g. a test host or an unusual vmnet setup.
func classifyGuestAddr(addr string, bridges []bridgeSubnet) (runtime.NetHealthState, string) {
	if len(bridges) == 0 {
		return runtime.NetHealthUnknown, "no host bridge interfaces found to verify " + addr + " is routable"
	}
	if ip := net.ParseIP(addr); ip != nil {
		for _, b := range bridges {
			if b.net.Contains(ip) {
				return runtime.NetHealthOK, addr
			}
		}
	}
	return runtime.NetHealthWedged, fmt.Sprintf(
		"stale DHCP lease: guest has %s but no host bridge is on that subnet (%s)",
		addr, describeBridges(bridges))
}

// describeBridges renders the host's bridge subnets for the wedged-detail
// message, e.g. "bridge100 is 192.168.139.3/23".
func describeBridges(bridges []bridgeSubnet) string {
	parts := make([]string, len(bridges))
	for i, b := range bridges {
		parts[i] = fmt.Sprintf("%s is %s", b.name, b.net.String())
	}
	return strings.Join(parts, ", ")
}
