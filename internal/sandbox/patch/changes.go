// ABOUTME: DetectChanges and HasUnappliedWork: git-status helpers shared by
// ABOUTME: the sandbox inspect, create, and lifecycle packages.
package patch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DetectChanges checks if the sandbox work directory has uncommitted changes.
// Returns "yes" if changes exist, "no" if clean, "-" if not applicable.
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
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if len(line) < 3 {
			continue
		}
		name := filepath.Base(line[3:])
		if strings.HasPrefix(name, "yoloai-bugreport-") &&
			(strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".md.tmp")) {
			continue
		}
		return "yes"
	}
	return "no"
}

// HasUnappliedWork checks if a work directory has any unapplied work:
// uncommitted changes OR commits beyond the baseline SHA.
// Returns true if work exists that would be lost on destruction.
func HasUnappliedWork(workDir, baselineSHA string) bool {
	if DetectChanges(workDir) == "yes" {
		return true
	}
	if baselineSHA == "" {
		return false
	}
	// Check for commits beyond the baseline
	cmd := exec.Command("git", "-C", workDir, "rev-list", "--count", baselineSHA+"..HEAD") //nolint:gosec // G204: workDir and baselineSHA are sandbox-controlled
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != "0"
}
