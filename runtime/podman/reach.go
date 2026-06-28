// ABOUTME: Podman opts out of injector reachability for now — rootless podman's
// ABOUTME: bridge gateway isn't host-bindable; the slirp host-loopback path is unwired.
package podman

import (
	"context"

	"github.com/kstenerud/yoloai/runtime"
)

// InjectorReach overrides the embedded docker.Runtime's so Podman does not broker
// yet. Rootless Podman's bridge gateway lives inside the rootless network
// namespace and is not host-bindable; the safe path is
// slirp4netns:allow_host_loopback → {BindHost:127.0.0.1, DialHost:10.0.2.2},
// which is not yet wired (see egress-broker-host-reachability.md). Until then
// Podman returns ErrInjectorUnsupported, so brokering auto-falls-back to direct
// delivery and an explicit --broker errors — rather than the injector failing to
// bind the unbindable gateway.
func (r *Runtime) InjectorReach(_ context.Context) (runtime.InjectorReach, error) {
	return runtime.InjectorReach{}, runtime.ErrInjectorUnsupported
}
