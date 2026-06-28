// ABOUTME: InjectorReach for apple — the shared vmnet "default" network's gateway
// ABOUTME: is both where the host injector binds and what the in-guest agent dials.
package apple

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/kstenerud/yoloai/runtime"
)

var _ runtime.InjectorReachable = (*Runtime)(nil)

// defaultNetwork is the apple `container` backend's built-in vmnet network; every
// sandbox attaches to it and its gateway is the guest's default route.
const defaultNetwork = "default"

// InjectorReach reports how an apple-container sandbox reaches a host-side
// injector. Apple's `container` backend puts every VM on a SHARED vmnet network
// ("default", NAT mode) whose gateway (e.g. 192.168.64.1) is a real host
// interface — bindable by a host process and reachable from the guest as its
// default route. So the agent dials the gateway and the injector binds the same
// IP (gateway-IP-for-both, like Linux Docker Engine / containerd). The vmnet
// subnet is VM-only (distinct from the host LAN), so binding the gateway does not
// expose the injector on the LAN. Verified on the 2026-06-28 Mac spike: a host
// process binds 192.168.64.1 and the guest curls it successfully, while the
// guest's own 127.0.0.1 does NOT reach the host (so a loopback bind would fail).
//
// Unlike tart's per-VM bridge, the shared vmnet bridge persists while the
// `container` system service runs, so the gateway is host-bindable BEFORE any of
// our VMs is created — the broker can start the injector ahead of the sandbox,
// independent of the launch path. On a host where the bridge is not up (the
// system service stopped, or it has never run) the gateway is assigned to no
// interface; InjectorReach then returns ErrInjectorUnsupported so brokering safely
// degrades to direct delivery rather than failing to bind.
func (r *Runtime) InjectorReach(ctx context.Context) (runtime.InjectorReach, error) {
	gw, err := r.vmnetGateway(ctx)
	if err != nil {
		return runtime.InjectorReach{}, err
	}
	if !ipAssignedToHost(gw) {
		return runtime.InjectorReach{}, runtime.ErrInjectorUnsupported
	}
	return runtime.InjectorReach{BindHost: gw, DialHost: gw}, nil
}

// vmnetGateway returns the IPv4 gateway of the apple "default" vmnet network,
// read from the CLI. It is a property of the network (knowable before any VM is
// created), not of any one container.
func (r *Runtime) vmnetGateway(ctx context.Context) (string, error) {
	out, err := r.runContainer(ctx, "network", "inspect", defaultNetwork)
	if err != nil {
		return "", fmt.Errorf("inspect %q network: %w", defaultNetwork, err)
	}
	return parseNetworkGateway(out)
}

// parseNetworkGateway extracts the IPv4 gateway from `container network inspect`
// JSON (an array; the gateway lives at [0].status.ipv4Gateway, the same nested
// shape as Inspect's status). Pure, so the extraction is unit-tested.
func parseNetworkGateway(jsonOut string) (string, error) {
	var arr []struct {
		Status struct {
			IPv4Gateway string `json:"ipv4Gateway"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &arr); err != nil {
		return "", fmt.Errorf("parse network inspect JSON: %w", err)
	}
	if len(arr) == 0 || arr[0].Status.IPv4Gateway == "" {
		return "", fmt.Errorf("network inspect reported no IPv4 gateway")
	}
	return arr[0].Status.IPv4Gateway, nil
}

// ipAssignedToHost reports whether ip is currently assigned to a host network
// interface — the signal that the vmnet bridge exists and its gateway is bindable.
// Mirrors containerd's identically-named check for its CNI bridge.
func ipAssignedToHost(ip string) bool {
	target := net.ParseIP(ip)
	if target == nil {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(target) {
			return true
		}
	}
	return false
}
