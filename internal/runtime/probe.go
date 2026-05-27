// ABOUTME: Backend availability probing and container-backend selection — the
// ABOUTME: registry-level glue that lets generic callers ask "is X usable?" or
// ABOUTME: "which container backend should I use?" without naming concrete packages.

package runtime

import (
	"context"
	"fmt"
)

// Probe reports whether the named backend is usable right now. Returns
// (false, "<reason>") when the backend is not registered on this platform
// or its Probe says no. A backend whose descriptor has no Probe is treated
// as always available (true, "").
//
// Distinct from IsAvailable: IsAvailable is static — "compiled in for this
// platform" — while Probe is dynamic — "the daemon/socket/binary it needs
// is actually present right now".
func Probe(ctx context.Context, name string) (available bool, reason string) {
	desc, ok := Descriptor(name)
	if !ok {
		return false, fmt.Sprintf("backend %q is not available on this platform", name)
	}
	if desc.Probe == nil {
		return true, ""
	}
	return desc.Probe(ctx)
}

// SelectContainerBackend picks the best available container backend
// (`BaseModeName == "container"`). It tries `preferred` first when non-empty
// and registered, then falls back to any other available container backend,
// in alphabetical order.
//
// If a preferred backend is named but not probe-available, the returned
// warning string explains the fallback. If no container backend is available
// at all, the returned name is the preferred one (or the first candidate
// alphabetically), so the caller fails downstream in `runtime.New` with a
// clear backend-specific error rather than a generic "no backend" message.
func SelectContainerBackend(ctx context.Context, preferred string) (backend string, warning string) {
	candidates := containerBackends()
	if len(candidates) == 0 {
		// No container backends registered on this platform (e.g. macOS without
		// docker/podman). The caller's next runtime.New() will surface the real
		// error; we just pick a stable name so the error path is deterministic.
		if preferred != "" {
			return preferred, ""
		}
		return "docker", ""
	}

	// Move preferred to the front of the candidate list if it's a known
	// container backend. Otherwise the user typed a name that doesn't match
	// any container backend (e.g. "tart") — ignore the preference silently;
	// preference is for the docker/podman slot and "tart" isn't in it.
	ordered := orderCandidates(candidates, preferred)

	// Try each candidate in order.
	for _, name := range ordered {
		ok, _ := Probe(ctx, name)
		if ok {
			if preferred != "" && name != preferred {
				warning = fmt.Sprintf("Warning: container_backend=%s not available; falling back to %s", preferred, name)
			}
			return name, warning
		}
	}

	// Nothing available. Return the preferred (or first) candidate so the
	// caller's runtime.New fails with the backend-specific error message.
	if preferred != "" && contains(candidates, preferred) {
		return preferred, ""
	}
	return ordered[0], ""
}

// containerBackends returns the names of all registered backends whose
// BaseModeName is "container" (docker, podman; not containerd's vm mode).
func containerBackends() []string {
	var out []string
	for _, d := range Descriptors() {
		if d.BaseModeName == "container" {
			out = append(out, d.Name)
		}
	}
	return out
}

// orderCandidates returns candidates with preferred moved to the front when
// it's in the list. candidates is already sorted alphabetically by
// Descriptors(), preserved for the non-preferred tail.
func orderCandidates(candidates []string, preferred string) []string {
	if preferred == "" || !contains(candidates, preferred) {
		return candidates
	}
	out := make([]string, 0, len(candidates))
	out = append(out, preferred)
	for _, c := range candidates {
		if c != preferred {
			out = append(out, c)
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
