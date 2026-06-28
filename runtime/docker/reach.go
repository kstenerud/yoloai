// ABOUTME: InjectorReach for docker (podman overrides it): the default bridge
// ABOUTME: gateway is both where the host injector binds and what the agent dials.
package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"

	"github.com/kstenerud/yoloai/runtime"
)

var _ runtime.InjectorReachable = (*Runtime)(nil)

// InjectorReach reports how a sandbox container reaches a host-side injector. On
// Linux Docker Engine the default bridge gateway is host-bindable and reachable
// from the container, so the agent dials it and the injector binds the same IP
// (the gateway-IP-for-both decision; Docker Desktop differs and is handled by a
// future macOS-aware variant — see the host-reachability research).
//
// The gateway is a property of the default bridge network, not of any one
// container, so it is knowable before a container is created. That lets the
// broker start the injector ahead of the container — independent of which launch
// path (agent-free or legacy) brings the box up.
func (r *Runtime) InjectorReach(ctx context.Context) (runtime.InjectorReach, error) {
	gw, err := r.bridgeGateway(ctx)
	if err != nil {
		return runtime.InjectorReach{}, err
	}
	return runtime.InjectorReach{BindHost: gw, DialHost: gw}, nil
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
