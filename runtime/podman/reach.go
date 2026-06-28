// ABOUTME: InjectorReach for podman — rootless reaches a loopback-bound injector
// ABOUTME: via slirp4netns's host alias (10.0.2.2); rootful is not wired yet.
package podman

import (
	"context"

	"github.com/kstenerud/yoloai/runtime"
)

// InjectorReach overrides the embedded docker.Runtime's. Rootless podman's bridge
// gateway lives inside the rootless network namespace and is not host-bindable,
// so gateway-for-both can't work; instead the injector binds the host loopback
// (127.0.0.1) and the sandbox — created with slirp4netns:allow_host_loopback —
// reaches it via slirp's fixed host alias 10.0.2.2 (verified, including through
// podman's docker-compat API). See egress-broker-host-reachability.md.
//
// Rootful podman uses a real host bridge (host-bindable, like docker) but that
// path isn't validated yet, so it reports ErrInjectorUnsupported (brokering →
// direct delivery) rather than guessing.
func (r *Runtime) InjectorReach(_ context.Context) (runtime.InjectorReach, error) {
	if !r.rootless {
		return runtime.InjectorReach{}, runtime.ErrInjectorUnsupported
	}
	return runtime.InjectorReach{
		BindHost:            "127.0.0.1",
		DialHost:            "10.0.2.2",
		RequiredNetworkMode: "slirp4netns:allow_host_loopback=true",
	}, nil
}
