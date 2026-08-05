// ABOUTME: Network-liveness probe for apple containers — detects the vmnet subnet
// ABOUTME: re-pick that strands a running sandbox on a dead epoch, so `ls` stops
// ABOUTME: reporting a healthy box that cannot reach anything. Report-only (DF172).

package apple

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// Compile-time check: apple can probe one sandbox's guest network health.
var _ runtime.SandboxNetHealthProber = (*Runtime)(nil)

// SandboxNetHealth implements runtime.SandboxNetHealthProber. Callers only
// invoke it for a sandbox they already know is running, so it does no
// existence check of its own — same contract as tart's.
//
// Unlike tart's probe this needs no guest exec at all, because apple records
// both halves of the answer host-side: `container inspect` reports the
// container's own `ipv4Address` **and** the `ipv4Gateway` it was given when it
// started. The wedge is then a pure host-side question — is that gateway still
// assigned to a host interface? — which is exactly the failure DF172 describes:
// the vmnet bridge is torn down and re-created on a different subnet, the
// container keeps its address and gateway from the old epoch, and nothing on
// the host answers that gateway any more.
func (r *Runtime) SandboxNetHealth(ctx context.Context, name string) (runtime.VMNetHealth, error) {
	instance := r.instanceName(name)
	out, err := r.runContainer(ctx, "inspect", instance)
	if err != nil {
		return runtime.VMNetHealth{
			SandboxName: r.sandboxName(instance),
			VMName:      instance,
			State:       runtime.NetHealthUnknown,
			Detail:      fmt.Sprintf("container inspect failed: %v", err),
		}, nil
	}
	addr, gateway, parseErr := parseContainerNetwork(out)
	state, detail := classifyAppleNetHealth(addr, gateway, parseErr, hostAssignedIPs())
	return runtime.VMNetHealth{
		SandboxName: r.sandboxName(instance),
		VMName:      instance,
		State:       state,
		Detail:      detail,
	}, nil
}

// parseContainerNetwork extracts the container's IPv4 address (CIDR form, e.g.
// "192.168.64.6/24") and its gateway from `container inspect` JSON. The array
// and the nested `status` are the same shape Inspect and parseNetworkGateway
// already rely on; the per-container network list is under status.networks.
// Pure, so the extraction is unit-tested against captured real output.
func parseContainerNetwork(jsonOut string) (addr, gateway string, err error) {
	var arr []struct {
		Status struct {
			Networks []struct {
				IPv4Address string `json:"ipv4Address"`
				IPv4Gateway string `json:"ipv4Gateway"`
			} `json:"networks"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &arr); err != nil {
		return "", "", fmt.Errorf("parse container inspect JSON: %w", err)
	}
	if len(arr) == 0 || len(arr[0].Status.Networks) == 0 {
		return "", "", fmt.Errorf("container inspect reported no networks")
	}
	n := arr[0].Status.Networks[0]
	if n.IPv4Address == "" || n.IPv4Gateway == "" {
		return "", "", fmt.Errorf("container inspect reported no IPv4 address or gateway")
	}
	return n.IPv4Address, n.IPv4Gateway, nil
}

// classifyAppleNetHealth is the pure decision core. hostIPs is the set of IPv4
// addresses currently assigned to host interfaces.
//
// It reports WEDGED only on positive evidence — a gateway no host interface
// answers, or an address outside its own gateway's subnet. Everything it cannot
// judge is UNKNOWN, never OK: a probe that guesses "healthy" when it cannot tell
// is worse than no probe, because it is the false-healthy report DF172 exists to
// remove.
func classifyAppleNetHealth(addrCIDR, gateway string, parseErr error, hostIPs []net.IP) (runtime.NetHealthState, string) {
	if parseErr != nil {
		return runtime.NetHealthUnknown, parseErr.Error()
	}
	ip, ipNet, err := net.ParseCIDR(addrCIDR)
	if err != nil {
		return runtime.NetHealthUnknown, fmt.Sprintf("unparseable container address %q", addrCIDR)
	}
	gw := net.ParseIP(gateway)
	if gw == nil {
		return runtime.NetHealthUnknown, fmt.Sprintf("unparseable gateway %q", gateway)
	}
	// An empty host-IP set means the enumeration failed, not that nothing is
	// assigned. Judging a wedge from it would turn one transient failure into a
	// fleet-wide false alarm.
	if len(hostIPs) == 0 {
		return runtime.NetHealthUnknown, "could not enumerate host interface addresses"
	}
	for _, h := range hostIPs {
		if h.Equal(gw) {
			if !ipNet.Contains(gw) {
				return runtime.NetHealthWedged, fmt.Sprintf(
					"container has %s but its gateway %s is outside that subnet", addrCIDR, gateway)
			}
			return runtime.NetHealthOK, ip.String()
		}
	}
	return runtime.NetHealthWedged, fmt.Sprintf(
		"stale vmnet epoch: container has %s with gateway %s, which is assigned to no host interface "+
			"(the vmnet bridge was re-created on a different subnet)", addrCIDR, gateway)
}

// hostAssignedIPs returns every IPv4 address currently assigned to a host
// interface. Errors yield nil (best effort), which classifyAppleNetHealth reads
// as "cannot judge" rather than as a wedge.
func hostAssignedIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil {
			continue
		}
		out = append(out, ipNet.IP)
	}
	return out
}

// instancePrefix / sandboxName / instanceName mirror tart's naming idiom: the
// status read-model hands this package a sandbox name, while the container is
// named with the principal's instance prefix.
func (r *Runtime) instancePrefix() string {
	return config.InstancePrefix(r.layout.Principal)
}

func (r *Runtime) sandboxName(instanceName string) string {
	return strings.TrimPrefix(instanceName, r.instancePrefix())
}

func (r *Runtime) instanceName(name string) string {
	return r.instancePrefix() + r.sandboxName(name)
}
