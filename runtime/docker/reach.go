// ABOUTME: InjectorReach for docker (podman overrides it): native Linux Engine
// ABOUTME: binds the bridge gateway; Desktop-class engines bind loopback + dial the alias.
package docker

import (
	"context"
	"fmt"
	goruntime "runtime"

	"github.com/docker/docker/api/types/network"

	"github.com/kstenerud/yoloai/runtime"
)

var _ runtime.InjectorReachable = (*Runtime)(nil)

// InjectorReach reports how a sandbox container reaches a host-side injector.
//
// Two realizations, by engine flavor:
//   - Native Linux Docker Engine: the default bridge gateway is a host interface,
//     bindable and reachable from the container, so the agent dials it and the
//     injector binds the same IP — the gateway-IP-for-both decision.
//   - Desktop-class engine (Docker Desktop / OrbStack / Colima / …): the daemon
//     runs inside a Linux VM, so the bridge gateway lives in that VM and the host
//     can NOT bind it. The injector binds host loopback (127.0.0.1 — the safest
//     interface, never LAN-visible; Desktop/OrbStack NAT container→host traffic
//     onto it) and the agent dials host.docker.internal, which every Desktop-class
//     engine resolves to the host without an explicit --add-host. This is the only
//     case where BindHost≠DialHost — the reason the InjectorReach split exists.
//     Verified on OrbStack (2026-06-28 Mac spike; see the host-reachability research).
//
// The endpoint is a network-level property (the default bridge / the host
// loopback), not a property of any one container, so it is knowable before a
// container is created. That lets the broker start the injector ahead of the
// container — independent of which launch path (agent-free or legacy) brings the
// box up.
func (r *Runtime) InjectorReach(ctx context.Context) (runtime.InjectorReach, error) {
	if r.isDesktopClassEngine() {
		return runtime.InjectorReach{BindHost: "127.0.0.1", DialHost: "host.docker.internal"}, nil
	}
	gw, err := r.bridgeGateway(ctx)
	if err != nil {
		return runtime.InjectorReach{}, err
	}
	return runtime.InjectorReach{BindHost: gw, DialHost: gw}, nil
}

// isDesktopClassEngine reports whether the Docker daemon runs inside a Linux VM
// (Docker Desktop, OrbStack, Colima, …) rather than as a native host engine —
// the distinction that decides whether the bridge gateway is host-bindable.
//
// On macOS the daemon is ALWAYS in a VM regardless of provider (a Linux kernel
// must be virtualized), so darwin alone implies desktop-class. On Linux it is
// desktop-class only when one of the known provider sockets is present
// (providerNames, detected at construction); a bare native Engine has none and
// binds the bridge gateway directly.
func (r *Runtime) isDesktopClassEngine() bool {
	return goruntime.GOOS == "darwin" || len(r.providerNames) > 0
}

// bridgeGateway returns the IPv4 gateway of the Docker default bridge network
// (e.g. 172.17.0.1). yoloai attaches brokered sandboxes to the default bridge
// (brokering is skipped under network isolation), so this is the gateway the
// sandbox's traffic egresses through and a host process can bind.
func (r *Runtime) bridgeGateway(ctx context.Context) (string, error) {
	net, err := r.client.NetworkInspect(ctx, "bridge", network.InspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspect default bridge network: %w", err)
	}
	if gw := firstGateway(net.IPAM.Config); gw != "" {
		return gw, nil
	}
	return "", fmt.Errorf("default bridge network has no gateway")
}

// firstGateway returns the first non-empty gateway in a network's IPAM config
// (the IPv4 entry in practice). Pure, so the selection is unit-tested.
func firstGateway(cfgs []network.IPAMConfig) string {
	for _, cfg := range cfgs {
		if cfg.Gateway != "" {
			return cfg.Gateway
		}
	}
	return ""
}
