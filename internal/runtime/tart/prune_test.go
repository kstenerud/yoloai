//go:build !windows

// ABOUTME: Unit tests for tart Prune (orphan sweep) and PruneCache (--cache),
// ABOUTME: driven by a fake tart binary so no real VM or tart CLI is needed.

package tart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/stretchr/testify/require"
)

// fakeTart writes a stub `tart` executable that answers `list --quiet` from a
// static inventory file and records every `delete <name>` to a log. `stop` and
// anything else succeed silently. Returns a Runtime wired to the stub plus the
// path of the delete log so tests can assert what was removed.
func fakeTart(t *testing.T, vms []string) (*Runtime, string) {
	t.Helper()
	dir := t.TempDir()
	listFile := filepath.Join(dir, "inventory")
	deleteLog := filepath.Join(dir, "deleted")
	require.NoError(t, os.WriteFile(listFile, []byte(strings.Join(vms, "\n")+"\n"), 0600))

	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  list) cat %q ;;
  delete) echo "$2" >> %q ;;
  stop) : ;;
esac
`, listFile, deleteLog)
	binPath := filepath.Join(dir, "tart")
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0700)) //nolint:gosec // test stub must be executable

	layout := config.NewLayout(filepath.Join(dir, ".yoloai"))
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0700))
	return &Runtime{tartBin: binPath, layout: layout, homeDir: dir}, deleteLog
}

func deletedNames(t *testing.T, deleteLog string) []string {
	t.Helper()
	data, err := os.ReadFile(deleteLog) //nolint:gosec // path is test-controlled
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

// --- orphan sweep ---------------------------------------------------------

func TestPruneRemovesOrphanButKeepsBase(t *testing.T) {
	// Inventory: the base template, one known sandbox, one orphan, plus an
	// unrelated VM that doesn't carry the yoloai- prefix.
	r, deleteLog := fakeTart(t, []string{
		provisionedImageName, // yoloai-base — must survive
		"yoloai-keep",        // known sandbox — must survive
		"yoloai-orphan",      // orphan — must be removed
		"some-other-vm",      // not ours — must be ignored
	})

	result, err := r.Prune(context.Background(), []string{"yoloai-keep"}, false, os.Stderr)
	require.NoError(t, err)

	require.Equal(t, []string{"yoloai-orphan"}, deletedNames(t, deleteLog))
	require.Len(t, result.Items, 1)
	require.Equal(t, "yoloai-orphan", result.Items[0].Name)
}

func TestPruneDryRunDeletesNothing(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{provisionedImageName, "yoloai-orphan"})

	result, err := r.Prune(context.Background(), nil, true, os.Stderr)
	require.NoError(t, err)

	require.Empty(t, deletedNames(t, deleteLog))
	require.Len(t, result.Items, 1) // reported, not removed
	require.Equal(t, "yoloai-orphan", result.Items[0].Name)
}

// --- cache reclaim --------------------------------------------------------

func TestPruneCacheRemovesBaseAndChecksum(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{provisionedImageName, defaultBaseImage, "yoloai-keep"})

	checksum := r.tartBaseChecksumPath()
	require.NoError(t, os.WriteFile(checksum, []byte("deadbeef"), 0600))

	require.NoError(t, r.PruneCache(context.Background(), false, os.Stderr))

	deleted := deletedNames(t, deleteLog)
	require.Contains(t, deleted, provisionedImageName)
	require.Contains(t, deleted, defaultBaseImage)
	require.NotContains(t, deleted, "yoloai-keep") // sandboxes are not cache
	require.NoFileExists(t, checksum)
}

func TestPruneCacheDryRunKeepsEverything(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{provisionedImageName, defaultBaseImage})

	checksum := r.tartBaseChecksumPath()
	require.NoError(t, os.WriteFile(checksum, []byte("deadbeef"), 0600))

	require.NoError(t, r.PruneCache(context.Background(), true, os.Stderr))

	require.Empty(t, deletedNames(t, deleteLog))
	require.FileExists(t, checksum)
}
