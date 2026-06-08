//go:build !windows

// ABOUTME: Unit tests for tart Prune (orphan sweep) and PruneCache (--images),
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

// fakeEntry is one row of the stub tart inventory: a VM or OCI image with its
// source ("local"/"OCI") and whole-GB on-disk size.
type fakeEntry struct {
	name   string
	source string
	sizeGB int
}

// fakeTart writes a stub `tart` executable from a plain name list, inferring
// each entry's source (OCI when the name contains "/", else local) and giving
// it a 1 GB size. For tests that only assert which names get deleted.
func fakeTart(t *testing.T, vms []string) (*Runtime, string) {
	t.Helper()
	entries := make([]fakeEntry, len(vms))
	for i, name := range vms {
		source := "local"
		if strings.Contains(name, "/") {
			source = "OCI"
		}
		entries[i] = fakeEntry{name: name, source: source, sizeGB: 1}
	}
	return fakeTartEntries(t, entries)
}

// fakeTartEntries writes a stub `tart` executable backed by a mutable inventory
// file. It answers `list --quiet` (names only) and `list --format json`
// (Name/Source/Size), records every `delete <name>` to a log AND removes that
// row from the inventory so a follow-up `list` reflects the deletion (lets the
// PruneCache before/after reclaim delta be exercised). `stop` is a no-op.
// Returns a Runtime wired to the stub plus the delete-log path.
func fakeTartEntries(t *testing.T, entries []fakeEntry) (*Runtime, string) {
	t.Helper()
	dir := t.TempDir()
	invFile := filepath.Join(dir, "inventory")
	deleteLog := filepath.Join(dir, "deleted")

	var lines []string
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("%s|%s|%d", e.name, e.source, e.sizeGB))
	}
	require.NoError(t, os.WriteFile(invFile, []byte(strings.Join(lines, "\n")), 0600))

	script := fmt.Sprintf(`#!/bin/sh
INV=%q
case "$1" in
  list)
    case "$*" in
      *json*) awk -F'|' 'BEGIN{printf"["} $1!=""{if(n++)printf",";printf"{\"Name\":\"%%s\",\"Source\":\"%%s\",\"Size\":%%s}",$1,$2,$3} END{printf"]"}' "$INV" ;;
      *) awk -F'|' '$1!=""{print $1}' "$INV" ;;
    esac ;;
  delete)
    awk -F'|' -v n="$2" '$1!=n' "$INV" > "$INV.tmp" && mv "$INV.tmp" "$INV"
    echo "$2" >> %q ;;
  stop) : ;;
esac
`, invFile, deleteLog)
	binPath := filepath.Join(dir, "tart")
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0700)) //nolint:gosec // test stub must be executable

	layout := config.NewLayout(filepath.Join(dir, ".yoloai"))
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0700))
	// Pin the host major to tahoe (26) so resolveBaseImage deterministically
	// returns defaultBaseImage regardless of the machine running the test.
	return &Runtime{
		tartBin:   binPath,
		layout:    layout,
		homeDir:   dir,
		hostMajor: func() (int, error) { return 26, nil },
	}, deleteLog
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

	_, err := r.PruneCache(context.Background(), true /*includeImages*/, false /*dryRun*/, os.Stderr)
	require.NoError(t, err)

	deleted := deletedNames(t, deleteLog)
	require.Contains(t, deleted, provisionedImageName)
	require.Contains(t, deleted, defaultBaseImage)
	require.NotContains(t, deleted, "yoloai-keep") // sandboxes are not cache
	require.NoFileExists(t, checksum)
}

// Without --images tart has no no-rebuild cache to reclaim, so PruneCache is a
// no-op: the base image and provision checksum must survive (the invariant that
// plain `prune` never forces a rebuild).
func TestPruneCacheWithoutImagesIsNoOp(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{provisionedImageName, defaultBaseImage})

	checksum := r.tartBaseChecksumPath()
	require.NoError(t, os.WriteFile(checksum, []byte("deadbeef"), 0600))

	_, err := r.PruneCache(context.Background(), false /*includeImages*/, false /*dryRun*/, os.Stderr)
	require.NoError(t, err)

	require.Empty(t, deletedNames(t, deleteLog))
	require.FileExists(t, checksum)
}

func TestPruneCacheDryRunKeepsEverything(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{provisionedImageName, defaultBaseImage})

	checksum := r.tartBaseChecksumPath()
	require.NoError(t, os.WriteFile(checksum, []byte("deadbeef"), 0600))

	_, err := r.PruneCache(context.Background(), true /*includeImages*/, true /*dryRun*/, os.Stderr)
	require.NoError(t, err)

	require.Empty(t, deletedNames(t, deleteLog))
	require.FileExists(t, checksum)
}
