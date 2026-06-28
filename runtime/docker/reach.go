// ABOUTME: InjectorReach for docker (and podman, which embeds this Runtime): the
// ABOUTME: bridge gateway is both where the host injector binds and what the agent dials.
package docker

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"

	"github.com/kstenerud/yoloai/runtime"
)

var _ runtime.InjectorReachable = (*Runtime)(nil)

// InjectorReach reports how the named container reaches a host-side injector. On
// Linux Docker Engine the bridge gateway is host-bindable and reachable from the
// container, so the agent dials it and the injector binds the same IP (the
// gateway-IP-for-both decision; Docker Desktop differs and is handled by a future
// macOS-aware variant — see the host-reachability research).
func (r *Runtime) InjectorReach(ctx context.Context, name string) (runtime.InjectorReach, error) {
	info, err := r.client.ContainerInspect(ctx, name)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return runtime.InjectorReach{}, r.notFound()
		}
		return runtime.InjectorReach{}, fmt.Errorf("inspect container: %w", err)
	}

	gw := containerGateway(info.NetworkSettings)
	if gw == "" {
		return runtime.InjectorReach{}, fmt.Errorf("container %q has no network gateway (network not attached?)", name)
	}
	return runtime.InjectorReach{BindHost: gw, DialHost: gw}, nil
}

// containerGateway returns the gateway IP of the container's network — the first
// attached network that has one. yoloai attaches a single network (the default
// bridge), so the choice is unambiguous in practice; the top-level
// NetworkSettings.Gateway is avoided as it is deprecated (removed in Docker v29).
func containerGateway(ns *container.NetworkSettings) string {
	if ns == nil {
		return ""
	}
	for _, ep := range ns.Networks {
		if ep != nil && ep.Gateway != "" {
			return ep.Gateway
		}
	}
	return ""
}
