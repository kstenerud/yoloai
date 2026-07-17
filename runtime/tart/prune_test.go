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

	layout := config.NewLayout(filepath.Join(dir, ".yoloai")).WithPrincipal(config.CLIPrincipal)
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0700))
	// Pin the host major to tahoe (26) so resolveBaseImage deterministically
	// returns defaultBaseImage regardless of the machine running the test.
	return &Runtime{
		tartBin:   binPath,
		layout:    layout,
		homeDir:   dir,
		hostMajor: func() (int, error) { return 26, nil },
		// Explicit env (the test's edge, per DEV §12): the stub shells out to
		// awk/mv, so it needs PATH — but never the inherited ambient env.
		execEnv: []string{"PATH=/usr/bin:/bin"},
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
		"yoloai-cli-keep",    // known sandbox — must survive
		"yoloai-cli-orphan",  // orphan — must be removed
		"some-other-vm",      // not ours — must be ignored
	})

	result, err := r.Prune(context.Background(), []string{"yoloai-cli-keep"}, false, os.Stderr)
	require.NoError(t, err)

	require.Equal(t, []string{"yoloai-cli-orphan"}, deletedNames(t, deleteLog))
	require.Len(t, result.Items, 1)
	require.Equal(t, "yoloai-cli-orphan", result.Items[0].Name)
}

func TestPruneDryRunDeletesNothing(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{provisionedImageName, "yoloai-cli-orphan"})

	result, err := r.Prune(context.Background(), nil, true, os.Stderr)
	require.NoError(t, err)

	require.Empty(t, deletedNames(t, deleteLog))
	require.Len(t, result.Items, 1) // reported, not removed
	require.Equal(t, "yoloai-cli-orphan", result.Items[0].Name)
}

// TestPruneReclaimsLegacyCLIVMs covers DF125's tart half: a VM created before the
// CLI adopted the "cli" principal keeps its "yoloai-<name>" identity, and the
// migration cannot rename it (it only walks sandboxes that still have a sandbox
// dir; an orphan has none). The CLI's sweep must still reclaim it, or it holds a
// capped VM slot forever with no yoloai command able to name it.
func TestPruneReclaimsLegacyCLIVMs(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{
		provisionedImageName,       // yoloai-base — must survive
		"yoloai-cli-current",       // current-scheme orphan — reclaimed
		"yoloai-legacyorphan",      // pre-D126 orphan — reclaimed (DF125)
		"yoloai-legacy-hyphenated", // pre-D126 orphan whose name has a '-' — reclaimed
		"yoloai-cli-keep",          // known sandbox — must survive
		"yoloai-t0000001-testvm",   // a test principal's VM — must survive
		"some-other-vm",            // not ours — ignored
	})

	result, err := r.Prune(context.Background(), []string{"yoloai-cli-keep"}, false, os.Stderr)
	require.NoError(t, err)

	require.ElementsMatch(t,
		[]string{"yoloai-cli-current", "yoloai-legacyorphan", "yoloai-legacy-hyphenated"},
		deletedNames(t, deleteLog))
	require.Len(t, result.Items, 3)
}

// TestPruneSparesKnownLegacyVM is the safety pair for the sweep above: a sandbox
// the migration has NOT yet converted still has a live legacy-named VM, and it
// must survive. classifySandboxes names such a VM via store.LegacyCLIInstanceName
// so it lands in `known`. Without that, claiming legacy VMs would destroy a
// running sandbox that merely failed to migrate.
func TestPruneSparesKnownLegacyVM(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{"yoloai-unmigrated", "yoloai-legacyorphan"})

	result, err := r.Prune(context.Background(), []string{"yoloai-unmigrated"}, false, os.Stderr)
	require.NoError(t, err)

	require.Equal(t, []string{"yoloai-legacyorphan"}, deletedNames(t, deleteLog),
		"a known legacy-named VM is a live sandbox, not debris")
	require.Len(t, result.Items, 1)
}

// TestPruneLegacyMatchOverreachesForAnUnseenPrincipal pins the ONE compromise in
// DF125's tart half, so it cannot drift into a surprise. Tart stores no labels
// (DF124), so the legacy identity is a heuristic on the name, and the legacy form
// overlaps every principal namespace: "yoloai-acme-probe" is both a pre-D126
// sandbox named "acme-probe" and principal "acme"'s sandbox "probe". The matcher
// excludes the namespaces that have ACTUALLY existed (the CLI's own, and the test
// principals') and claims the rest — so an integrator running tart under a
// principal that has never shipped would be over-reached.
//
// If tart ever gains a real second principal, this test fails and forces the
// decision rather than letting a sweep quietly delete someone's VM.
func TestPruneLegacyMatchOverreachesForAnUnseenPrincipal(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{"yoloai-acme-probe"})

	_, err := r.Prune(context.Background(), nil, false, os.Stderr)
	require.NoError(t, err)

	require.Equal(t, []string{"yoloai-acme-probe"}, deletedNames(t, deleteLog),
		"documented over-reach: indistinguishable from a legacy CLI sandbox named \"acme-probe\"")
}

// TestPrunePrincipalScope verifies that a Runtime with a non-empty principal
// only sweeps VMs whose names carry its own prefix ("yoloai-<principal>-*"),
// leaving yoloai-base, other-principal VMs, and bare-yoloai VMs untouched.
// This is the DF19 structural backstop: test-scoped prune can never touch the
// developer's real resources — and the gate on DF125's legacy match, which is
// why a bare-yoloai VM still survives a non-CLI principal's sweep.
func TestPrunePrincipalScope(t *testing.T) {
	r, deleteLog := fakeTart(t, []string{
		provisionedImageName,  // yoloai-base — must survive (protected by name guard)
		"yoloai-tok01-orphan", // same-principal orphan — must be deleted
		"yoloai-other-vm",     // different-principal — must survive
		"yoloai-plain",        // no-principal — must survive (wrong prefix)
	})
	// Set the layout principal to "tok01".
	p, err := config.ParsePrincipalSegment("tok01")
	require.NoError(t, err)
	r.layout = r.layout.WithPrincipal(p)

	result, err := r.Prune(context.Background(), nil, false, os.Stderr)
	require.NoError(t, err)

	deleted := deletedNames(t, deleteLog)
	require.Equal(t, []string{"yoloai-tok01-orphan"}, deleted, "only the same-principal orphan must be deleted")
	require.Len(t, result.Items, 1)
	require.Equal(t, "yoloai-tok01-orphan", result.Items[0].Name)
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
