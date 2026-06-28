//go:build linux

// ABOUTME: InjectorReach for containerd — the yoloai CNI bridge gateway is both
// ABOUTME: where the host injector binds and what the agent (Kata guest) dials.
package containerdrt

import (
	"context"
	"net"

	"github.com/kstenerud/yoloai/runtime"
)

var _ runtime.InjectorReachable = (*Runtime)(nil)

// InjectorReach reports how a containerd sandbox reaches a host-side injector.
// The yoloai CNI bridge gateway (10.89.0.1) is host-bindable and reachable from
// the Kata guest (verified: the guest's default route is the gateway), so the
// agent dials it and the injector binds the same IP — gateway-IP-for-both, like
// Linux Docker Engine.
//
// The bridge (yoloai0) and its gateway are created during the first sandbox's CNI
// ADD, so on a host that has never run a yoloai containerd sandbox the gateway
// isn't yet bindable. Until then InjectorReach reports ErrInjectorUnsupported, so
// brokering safely degrades to direct delivery for that launch (rather than
// failing to bind) and engages on the next sandbox — the bridge persists once
// created. Eagerly ensuring the bridge before brokering is a follow-up (see
// egress-broker-host-reachability.md).
func (r *Runtime) InjectorReach(_ context.Context) (runtime.InjectorReach, error) {
	gw, err := cniGateway()
	if err != nil {
		return runtime.InjectorReach{}, err
	}
	if !ipAssignedToHost(gw) {
		return runtime.InjectorReach{}, runtime.ErrInjectorUnsupported
	}
	return runtime.InjectorReach{BindHost: gw, DialHost: gw}, nil
}

// ipAssignedToHost reports whether ip is currently assigned to a host network
// interface — the signal that the yoloai bridge exists and its gateway is
// bindable.
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
