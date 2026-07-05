// ABOUTME: The mandatory-infrastructure test policy (D112): a platform-possible
// ABOUTME: backend is required, so its absence FAILS loudly — never a silent skip.
// ABOUTME: The sole carve-out is YOLOAI_TEST_UNCONTROLLED_BACKENDS, a CSV of the
// ABOUTME: backends the current (uncontrolled) runner genuinely cannot provision.
package testutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// uncontrolledBackendsEnv names the backends the current runner cannot provision.
// It exists ONLY for uncontrolled environments we don't own (e.g. GitHub CI): a
// backend whose name is listed downgrades from FAIL to skip when its
// infrastructure is absent. Every backend NOT listed stays mandatory even in CI,
// so an unexpected loss of a provisioned backend (e.g. Docker vanishing) still
// fails loudly. Empty/unset — every dev machine — means every platform-possible
// backend is mandatory. The name signals intent, not permission: it should feel
// wrong to set on a machine you control. We deliberately do NOT auto-detect
// CI/GITHUB_ACTIONS, so a dev box exporting CI=1 never silently starts skipping.
const uncontrolledBackendsEnv = "YOLOAI_TEST_UNCONTROLLED_BACKENDS"

// UncontrolledBackends parses YOLOAI_TEST_UNCONTROLLED_BACKENDS once into a set of
// backend names (e.g. {"containerd": true, "apple": true}). It is the single
// source that BackendAbsent and RequireBackend consult, so there is exactly one
// env read and one grep target for the policy. Empty/unset yields an empty set —
// meaning nothing is carved out and every backend is mandatory.
func UncontrolledBackends() map[string]bool {
	out := map[string]bool{}
	for name := range strings.SplitSeq(os.Getenv(uncontrolledBackendsEnv), ",") { //nolint:forbidigo // policy env: THE single carve-out read
		if name = strings.TrimSpace(name); name != "" {
			out[name] = true
		}
	}
	return out
}

// BackendAbsent is the TestMain gate for a backend whose infrastructure was found
// absent. It returns the exit code TestMain should pass to os.Exit: 0 when the
// backend is carved out (the CI path — skip cleanly), else 1 (FAIL — the backend
// is possible on this host, so its absence is a misconfiguration to fix, not to
// hide). reason is the human-facing diagnostic (why the backend looked absent).
func BackendAbsent(name, reason string) int {
	if UncontrolledBackends()[name] {
		fmt.Fprintf(os.Stderr, "%s backend absent but carved out via %s — skipping integration tests: %s\n",
			name, uncontrolledBackendsEnv, reason)
		return 0
	}
	fmt.Fprintf(os.Stderr,
		"%s backend is unavailable on this host: %s\n"+
			"A platform-possible backend is mandatory (D112) — provision it or, only on an "+
			"uncontrolled runner, set %s=%s to carve it out.\n",
		name, reason, uncontrolledBackendsEnv, name)
	return 1
}

// RequireBackend is the per-test gate (used where availability is probed per test,
// e.g. containerd's socket/netns check) equivalent of BackendAbsent: it t.Skips
// when the backend is carved out, else t.Fatals. Absence of a platform-possible
// backend is a hard failure (D112), not a silent skip.
func RequireBackend(t *testing.T, name, reason string) {
	t.Helper()
	if UncontrolledBackends()[name] {
		t.Skipf("%s backend absent but carved out via %s: %s", name, uncontrolledBackendsEnv, reason)
	}
	t.Fatalf("%s backend is unavailable on this host: %s\n"+
		"A platform-possible backend is mandatory (D112) — provision it or, only on an "+
		"uncontrolled runner, set %s=%s to carve it out.", name, reason, uncontrolledBackendsEnv, name)
}
