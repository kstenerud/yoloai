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
// Unlike tart's per-VM bridge, the shared vmnet bridge persists once it exists —
// it outlives the container that created it, and further containers reuse it.
//
// It is NOT up merely because the system service is running. The bridge is created
// by the FIRST container start, so on a freshly started service the gateway is
// assigned to no interface (DF178, measured: `container system start` with no
// container ever run leaves 192.168.64.1 unassigned). An earlier version of this
// comment claimed the opposite and drew a conclusion from it — that the broker
// could start the injector ahead of the sandbox, independent of the launch path.
// That is the part that was false, and it is the only part anything depended on.
//
// What saves brokering today is ordering, not the bridge being early: the reach
// check runs after the container exists, by which point the bridge is up.
// Measured end to end on a bridgeless service with a credential present — an
// explicit `--broker` succeeded and left a live injector.json, so no silent
// degradation occurs on this path. **Anything that moves the injector earlier
// re-opens it**, which is exactly what the old comment invited.
//
// When the bridge genuinely is absent, InjectorReach returns ErrInjectorUnsupported
// and brokering degrades to direct delivery rather than failing to bind — the
// agent's key is then handed into the sandbox, so that path is a silent security
// downgrade and must stay unreachable on ordinary timing.
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
