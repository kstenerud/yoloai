// ABOUTME: Assembles the flat guest view over the sandbox tiers, for the two
// ABOUTME: backends that share a directory instead of binding each file.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// GuestViewDir returns the single directory a guest sees as its sandbox root.
//
// It is the read-write tier itself, not a fourth directory beside the three.
// The guest scripts join every path from one root (os.path.join(yoloai_dir,
// "logs", …)), so *some* directory has to hold both tiers' entries, and that
// directory is necessarily guest-writable — the guest creates root entries in
// it, including the on-create marker the host reads back. A separate view dir
// would therefore be an un-tiered place for new files to land, which is the one
// thing the three-tier layout exists to rule out. Using rw/ means an entry
// nobody classified lands in the fail-safe tier and stays visible to the host.
//
// The host never reads through the view. Every host-side path comes from the
// builders in sandbox_layout.go, which address the real tier — so a guest that
// deletes or replaces one of these links only breaks its own view, and cannot
// make the host read a file of its choosing. That asymmetry is what keeps DF148
// closed: the host reads ro/runtime-config.json, never rw/runtime-config.json.
func GuestViewDir(sandboxDir string) string { return ReadWriteTierDir(sandboxDir) }

// viewLinkTarget is the symlink body written for a read-only entry surfaced in
// the view. It is deliberately *relative*: the same link then resolves on the
// host (<sandboxDir>/rw/../ro/x) and inside a tart guest, where the two tiers
// are separate VirtioFS shares mounted as siblings under one directory and
// named for their tiers (/Volumes/My Shared Files/{ro,rw}). An absolute target
// could only ever be right on one side of that boundary.
func viewLinkTarget(name string) string {
	return filepath.Join("..", ReadOnlyTierName, name)
}

// AssembleGuestView surfaces every read-only-tier entry inside the view, so a
// guest joining paths from one root reaches both tiers. It is idempotent and is
// re-run on every launch: the read-only tier gains entries between create and
// start (prompt.txt, machine-id, secrets/) and loses them again afterwards.
//
// It enumerates ro/ rather than working from a list of names, so it cannot
// drift out of step with what the tier actually holds — the same reason the
// tier is a directory in the first place.
func AssembleGuestView(sandboxDir string) error {
	roDir := ReadOnlyTierDir(sandboxDir)
	viewDir := GuestViewDir(sandboxDir)
	for _, dir := range []string{roDir, viewDir} {
		if err := fileutil.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create tier %s: %w", dir, err)
		}
	}

	entries, err := os.ReadDir(roDir)
	if err != nil {
		return fmt.Errorf("read read-only tier: %w", err)
	}

	surfaced := make(map[string]bool, len(entries))
	for _, e := range entries {
		surfaced[e.Name()] = true
		if err := linkIntoView(filepath.Join(viewDir, e.Name()), viewLinkTarget(e.Name())); err != nil {
			return err
		}
	}

	return pruneViewLinks(viewDir, surfaced)
}

// linkIntoView points one view entry at its read-only-tier original, replacing
// a link that points elsewhere.
//
// A non-symlink already sitting at the path is left alone. That can only be the
// guest having replaced its own view entry with a file of its own, which costs
// the guest its access to the read-only original and costs the host nothing —
// so it is reported rather than repaired, because silently deleting whatever is
// there would make a guest-owned file disappear on the next launch.
func linkIntoView(link, target string) error {
	switch existing, err := os.Readlink(link); {
	case err == nil && existing == target:
		return nil
	case err == nil:
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("replace stale view link %s: %w", link, err)
		}
	default:
		if _, statErr := os.Lstat(link); statErr == nil {
			slog.Warn("guest view entry is not a link to the read-only tier; leaving it as it is",
				"entry", filepath.Base(link), "path", link)
			return nil
		}
	}

	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("link %s into the guest view: %w", filepath.Base(link), err)
	}
	return nil
}

// pruneViewLinks removes view links whose read-only-tier original is gone, so
// the view does not accumulate dangling entries across launches — secrets/ is
// staged for one launch and deleted once consumed, and resume-prompt.txt only
// exists across a restart.
//
// Only links this function's own shape wrote are removed. A symlink the guest
// made for its own purposes has some other target and is left alone.
func pruneViewLinks(viewDir string, surfaced map[string]bool) error {
	entries, err := os.ReadDir(viewDir)
	if err != nil {
		return fmt.Errorf("read guest view: %w", err)
	}
	for _, e := range entries {
		if surfaced[e.Name()] || e.Type()&os.ModeSymlink == 0 {
			continue
		}
		link := filepath.Join(viewDir, e.Name())
		if target, err := os.Readlink(link); err != nil || target != viewLinkTarget(e.Name()) {
			continue
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("remove stale view link %s: %w", link, err)
		}
	}
	return nil
}
