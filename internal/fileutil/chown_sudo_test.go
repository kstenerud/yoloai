// ABOUTME: Pins MkdirAll's actual ownership outcome under sudo — which levels it
// ABOUTME: takes for the invoking user and which it leaves alone. Root only.
package fileutil

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// The chown half of DF186, measured rather than reasoned about. Its sibling in
// chown_test.go pins which directories get named; this pins what actually lands
// on disk, and it needs real root plus a real SUDO_UID, so it skips everywhere
// else. A skip is honest here: the alternative is asserting the outcome from the
// code that produces it, which is how the original defect passed review.
func TestMkdirAll_UnderSudo_TakesWhatItCreatesAndLeavesTheRest(t *testing.T) {
	uid := SudoUID()
	if uid == -1 {
		t.Skip("needs root with SUDO_UID set: run as `sudo -E go test ./internal/fileutil/`")
	}

	top := t.TempDir()
	// a/b/c pre-exists and is deliberately given to root, so "left alone" is
	// distinguishable from "chowned to the invoking user" rather than both
	// looking the same.
	pre := filepath.Join(top, "a", "b", "c")
	require.NoError(t, os.MkdirAll(pre, 0o750))
	for _, p := range []string{filepath.Join(top, "a"), filepath.Join(top, "a", "b"), pre} {
		require.NoError(t, os.Lchown(p, 0, 0))
	}

	require.NoError(t, MkdirAll(filepath.Join(pre, "d", "e", "f"), 0o750))

	ownerOf := func(rel string) int {
		st, err := os.Stat(filepath.Join(top, rel))
		require.NoError(t, err)
		return int(st.Sys().(*syscall.Stat_t).Uid)
	}

	// Created by this call: taken for the invoking user, every level.
	for _, rel := range []string{"a/b/c/d", "a/b/c/d/e", "a/b/c/d/e/f"} {
		require.Equal(t, uid, ownerOf(rel), "%s was conjured by this call and must be the user's", rel)
	}
	// Pre-existing: untouched. Re-owning these would silently reassign a tree
	// the call did not create.
	for _, rel := range []string{"a", "a/b", "a/b/c"} {
		require.Equal(t, 0, ownerOf(rel), "%s already existed and must keep its owner", rel)
	}
}
