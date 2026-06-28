// ABOUTME: InjectorReach for tart — declares the backend injector-reachable but
// ABOUTME: reports unsupported, because tart's vmnet gateway isn't host-bindable pre-create.
package tart

import (
	"context"

	"github.com/kstenerud/yoloai/runtime"
)

var _ runtime.InjectorReachable = (*Runtime)(nil)

// InjectorReach reports that tart cannot currently host a sandbox-reachable
// injector, so brokering safely degrades to direct credential delivery (auto
// mode) or errors out (explicit --broker) rather than failing to bind.
//
// Why it can't broker yet — a pre-create binding gap, not a reachability one:
//
// tart runs each VM on Apple's default vmnet NAT network and the host-side bridge
// for it (e.g. 192.168.65.1) is created fresh PER VM, on boot, and torn down when
// the VM stops (verified on the 2026-06-28 Mac spike: the bridge appears only
// while the VM runs). The broker, however, starts the injector in brokerCredentials
// — which runs BEFORE Create/Start in LaunchContainer — so at bind time no tart VM
// is running and the gateway IP is assigned to no host interface; binding it fails
// with EADDRNOTAVAIL. The guest's own 127.0.0.1 is the guest's loopback and does
// not reach the host (spike-verified), so the host-loopback bind that works for
// the Desktop-class docker engines and seatbelt is not an option either.
//
// This contrasts with apple's `container` backend, whose vmnet bridge is a SHARED,
// persistent network (bindable before any of our VMs boots), and with containerd,
// whose CNI bridge persists after the first sandbox — both broker via gateway-for-
// both. Making tart broker needs the "eager network-prepare" follow-up: ensure a
// stable, host-bindable gateway before brokering (see the conclusion of
// egress-broker-host-reachability.md and the containerd eager-bridge-ensure item
// in egress-proxy-build.md). The seam is implemented now so that follow-up only has
// to swap this body for a gateway-for-both reach.
func (r *Runtime) InjectorReach(_ context.Context) (runtime.InjectorReach, error) {
	return runtime.InjectorReach{}, runtime.ErrInjectorUnsupported
}
