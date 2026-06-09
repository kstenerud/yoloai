// ABOUTME: All apply operations (CheckPatch, ApplyPatch, ApplyFormatPatch,
// ABOUTME: CommitInfo, PatchSet, ContiguousPrefixEnd) have moved to internal/git.
// ABOUTME: This file re-exports types and free-function wrappers for deferred callers.
package workspace

import "github.com/kstenerud/yoloai/internal/git"

// Re-export types from internal/git for callers that haven't migrated yet.

// CommitInfo holds a commit SHA and its subject line.
type CommitInfo = git.CommitInfo

// PatchSet holds patch data for a single directory.
type PatchSet = git.PatchSet

// ContiguousPrefixEnd finds how far the baseline can safely advance.
// Delegates to git.ContiguousPrefixEnd.
func ContiguousPrefixEnd(allCommits []CommitInfo, appliedSHAs map[string]bool) int {
	return git.ContiguousPrefixEnd(allCommits, appliedSHAs)
}
