// ABOUTME: Tests for EnrichExitError — that a captured exit stderr is folded
// ABOUTME: into the message while the *exec.ExitError chain stays matchable.

package sysexec

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// shEnv is a minimal explicit env that still resolves `sh` and a bogus binary
// name on this host. sysexec forbids a nil env (DEV §12), so tests pass one.
var shEnv = []string{"PATH=/bin:/usr/bin"}

func TestEnrichExitError_NilPassesThrough(t *testing.T) {
	if got := EnrichExitError(nil); got != nil {
		t.Fatalf("EnrichExitError(nil) = %v, want nil", got)
	}
}

func TestEnrichExitError_FoldsCapturedStderr(t *testing.T) {
	// Output() leaves Stderr nil, so the runtime captures it into
	// (*exec.ExitError).Stderr — exactly the value the bare "%w" discards.
	_, err := Command(shEnv, "sh", "-c", "echo boom-diagnostic >&2; exit 3").Output()
	if err == nil {
		t.Fatal("expected a non-zero exit error")
	}

	got := EnrichExitError(err)
	if !strings.Contains(got.Error(), "boom-diagnostic") {
		t.Fatalf("enriched error %q does not surface the captured stderr", got)
	}

	// The wrap must not sever the chain: callers still match the exit code.
	exitErr, ok := errors.AsType[*exec.ExitError](got)
	if !ok {
		t.Fatal("errors.AsType[*exec.ExitError] no longer matches through the wrap")
	}
	if exitErr.ExitCode() != 3 {
		t.Fatalf("ExitCode() = %d, want 3", exitErr.ExitCode())
	}
}

func TestEnrichExitError_EmptyStderrReturnsUnchanged(t *testing.T) {
	// A non-zero exit that wrote nothing to stderr has nothing to add, so the
	// original error passes through untouched (no dangling ": ").
	_, err := Command(shEnv, "sh", "-c", "exit 4").Output()
	if err == nil {
		t.Fatal("expected a non-zero exit error")
	}
	if got := EnrichExitError(err); got.Error() != err.Error() {
		t.Fatalf("EnrichExitError = %q, want the original error unchanged", got)
	}
}

func TestEnrichExitError_FailToStartReturnsUnchanged(t *testing.T) {
	// A binary that never starts yields *exec.Error, not *exec.ExitError — there
	// is no captured stderr, so the error must pass through so its own "%w"
	// (which names the missing binary) survives.
	_, err := Command(shEnv, "yoloai-no-such-binary-df145").Output()
	if err == nil {
		t.Fatal("expected a start failure")
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		t.Fatal("test precondition: a start failure must not be an *exec.ExitError")
	}
	if got := EnrichExitError(err); got.Error() != err.Error() {
		t.Fatalf("EnrichExitError = %q, want the original error unchanged", got)
	}
}
