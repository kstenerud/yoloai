// ABOUTME: DetectChanges (host probe) and HasUnappliedWorkVia (runtime-aware
// ABOUTME: probe): git-status helpers shared by inspect, create, and lifecycle.
package patch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// WorkProbe is the outcome of probing a sandbox work dir for unapplied work.
type WorkProbe int

const (
	// WorkClean: the probe confirmed no unapplied work.
	WorkClean WorkProbe = iota
	// WorkDirty: the probe found unapplied work — uncommitted changes or commits
	// beyond the baseline.
	WorkDirty
	// WorkUnknown: the state could not be determined because the working copy
	// lives in a backend whose execution context is unavailable (e.g. a Tart VM
	// that is not running). Callers must fail safe and treat it as possibly dirty.
	WorkUnknown
)

// DetectChanges checks if the sandbox work directory has uncommitted changes by
// running git on the HOST. It is the metadata-free probe used by ProbeWorkData
// for broken sandboxes; gates that know the backend use HasUnappliedWorkVia,
// which runs git in the backend's execution context. Returns "yes", "no", or
// "-" (not a git repo / not applicable).
func DetectChanges(workDir string) string {
	if _, err := os.Stat(workDir); err != nil {
		return "-"
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return "-"
	}
	cmd := exec.Command("git", "-C", workDir, "status", "--porcelain") //nolint:gosec // G204: workDir is sandbox-controlled path
	output, err := cmd.Output()
	if err != nil {
		return "-"
	}
	if porcelainHasChange(string(output)) {
		return "yes"
	}
	return "no"
}

// porcelainHasChange reports whether `git status --porcelain` output carries a
// meaningful change, ignoring yoloai bug-report scratch files.
func porcelainHasChange(output string) bool {
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if len(line) < 3 {
			continue
		}
		name := filepath.Base(line[3:])
		if strings.HasPrefix(name, "yoloai-bugreport-") &&
			(strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".md.tmp")) {
			continue
		}
		return true
	}
	return false
}

// HasUnappliedWorkVia reports whether a work directory holds unapplied work —
// uncommitted changes OR commits beyond baselineSHA — running git in the
// backend's execution context (in-VM for VM-local backends like Tart, on the
// host otherwise) via runtime.GitExecFor. When that context is unavailable —
// the backend reports the instance is not running — it returns WorkUnknown so
// callers fail safe rather than read a stale host seed copy the VM never wrote
// back to (see backend-idiosyncrasies.md "VirtioFS corrupts git repositories").
func HasUnappliedWorkVia(ctx context.Context, rt runtime.Runtime, name, workDir, baselineSHA string) WorkProbe {
	out, err := runtime.GitExecFor(ctx, rt, name, workDir, "status", "--porcelain")
	if err != nil {
		if errors.Is(err, runtime.ErrNotRunning) {
			return WorkUnknown
		}
		// A non-repo or otherwise unreadable workdir mirrors the historical host
		// behavior of "no detectable work" (DetectChanges returned "-").
		return WorkClean
	}
	if porcelainHasChange(out) {
		return WorkDirty
	}
	if baselineSHA == "" {
		return WorkClean
	}
	out, err = runtime.GitExecFor(ctx, rt, name, workDir, "rev-list", "--count", baselineSHA+"..HEAD")
	if err != nil {
		if errors.Is(err, runtime.ErrNotRunning) {
			return WorkUnknown
		}
		return WorkClean
	}
	if strings.TrimSpace(out) != "0" {
		return WorkDirty
	}
	return WorkClean
}
