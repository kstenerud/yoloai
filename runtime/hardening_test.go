package runtime

import (
	"slices"
	"strings"
	"testing"
)

// GitHardeningArgs is prepended to every git invocation yoloai runs against
// agent-controlled content. It must neutralize the config-driven code-execution
// vectors that fire even for read-only ops (hooks, fsmonitor) without disabling
// attribute-bound filter/textconv drivers (which diff correctness depends on).
func TestGitHardeningArgs(t *testing.T) {
	args := GitHardeningArgs()

	// Flags come in -c/value pairs; collect the values for assertions.
	var settings []string
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] != "-c" {
			t.Fatalf("expected -c at index %d, got %q (args: %v)", i, args[i], args)
		}
		settings = append(settings, args[i+1])
	}

	// hooks: an agent-planted .git/hooks script must not run (audit C1).
	if !slices.Contains(settings, "core.hooksPath=/dev/null") {
		t.Errorf("hardening must disable hooks; got %v", settings)
	}
	// fsmonitor: an agent-planted core.fsmonitor=<command> must not run. This
	// fires even on `git status`, so it is a read-path RCE vector.
	if !slices.Contains(settings, "core.fsmonitor=false") {
		t.Errorf("hardening must disable fsmonitor; got %v", settings)
	}

	// Must NOT globally disable filters: those are attribute-bound and required
	// for diff correctness (LFS/git-crypt) where git runs in-confinement.
	for _, s := range settings {
		if strings.HasPrefix(s, "filter.") {
			t.Errorf("hardening must not disable attribute-bound filters globally; got %q", s)
		}
	}
}
