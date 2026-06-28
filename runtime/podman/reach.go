// ABOUTME: InjectorReach for podman — Linux rootless reaches a loopback-bound
// ABOUTME: injector via slirp's host alias (10.0.2.2); macOS/rootful are unsupported.
package podman

import (
	"context"
	goruntime "runtime"

	"github.com/kstenerud/yoloai/runtime"
)

// InjectorReach overrides the embedded docker.Runtime's. Only Linux rootless
// podman currently hosts a sandbox-reachable injector:
//
//   - macOS: podman runs inside a podman-machine Linux VM, so neither the slirp
//     alias (10.0.2.2 reaches the *machine VM's* host, not the Mac) nor the
//     gateway is host-bindable on the Mac. The machine's gvproxy host-forward
//     (192.168.127.254) IS network-reachable to a host-bound injector — a one-shot
//     curl through it succeeds — but it does NOT carry the real agent's traffic
//     reliably: a brokered Claude agent on podman-macOS hangs on its first API call
//     (gvproxy stalls the agent's sustained/streaming connection), where the same
//     agent on docker (host.docker.internal) and apple (vmnet gateway) brokers
//     fine. So podman reports ErrInjectorUnsupported on macOS and brokering degrades
//     to direct delivery — the conservative posture (also used for tart), restoring
//     the working pre-broker behavior. Making podman-macOS broker is a follow-up
//     (needs a host hop that survives streaming — see egress-broker-host-reachability.md).
//   - Linux rootless: the bridge gateway lives inside the rootless network
//     namespace and is not host-bindable, so gateway-for-both can't work; instead
//     the injector binds host loopback (127.0.0.1) and the sandbox — created with
//     slirp4netns:allow_host_loopback — reaches it via slirp's fixed host alias
//     10.0.2.2 (verified, including through podman's docker-compat API).
//   - Linux rootful: a real host bridge (host-bindable, like docker) but that path
//     isn't validated yet, so it reports ErrInjectorUnsupported (brokering →
//     direct delivery) rather than guessing.
//
// See egress-broker-host-reachability.md.
func (r *Runtime) InjectorReach(_ context.Context) (runtime.InjectorReach, error) {
	if goruntime.GOOS == "darwin" {
		return runtime.InjectorReach{}, runtime.ErrInjectorUnsupported
	}
	if !r.rootless {
		return runtime.InjectorReach{}, runtime.ErrInjectorUnsupported
	}
	return runtime.InjectorReach{
		BindHost:            "127.0.0.1",
		DialHost:            "10.0.2.2",
		RequiredNetworkMode: "slirp4netns:allow_host_loopback=true",
	}, nil
}
