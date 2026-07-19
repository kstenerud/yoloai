// ABOUTME: OwnershipAudit — the library half of `yoloai doctor`'s check for
// root-owned leftovers (from a `sudo yoloai …` run or backend VM/overlay state)
// that block prune, destroy, and image builds until chowned back.

package yoloai

import (
	"context"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// OwnershipMaxSample bounds how many offending paths OwnershipAudit records per
// concern — doctor prints these and a total, like the reclaimable-now section.
const OwnershipMaxSample = 10

// OwnershipConcern reports a directory holding files not owned by the invoking
// user. Such entries — typically root-owned leftovers from a `sudo yoloai …`
// run, or backend VM/overlay state the backend wrote as root — block the user
// from deleting, pruning, or rebuilding until ownership is restored. The remedy
// is `sudo chown -R ExpectedUID:ExpectedGID Root`.
type OwnershipConcern struct {
	Label       string   `json:"label"`            // human label, e.g. "Docker config"
	Root        string   `json:"root"`             // the scanned directory
	ExpectedUID int      `json:"expected_uid"`     // the uid that should own it
	ExpectedGID int      `json:"expected_gid"`     // paired with the uid for the chown remedy
	Count       int      `json:"count"`            // wrong-owned entries; a wrong-owned subtree counts once
	Sample      []string `json:"sample,omitempty"` // up to OwnershipMaxSample offending paths, top-most first
}

// OwnershipAudit reports directories the library can reach that hold files not
// owned by the invoking user. Today that is the Docker config dir a build uses,
// resolved from the layout's curated env (DOCKER_CONFIG, else HOME/.docker) so
// the audit inspects the same tree — including its buildx/ subdir — whose
// root-owned state broke image builds in DF144. It is read-only and returns only
// directories that actually hold a concern.
//
// The yoloai *data* tree lives under the CLI-owned TOP dir, which the library is
// forbidden to name (D60/D61); `yoloai doctor` audits that itself via
// fileutil.ScanWrongOwner. This verb covers what needs the library's env
// resolution.
func (s *System) OwnershipAudit(ctx context.Context) ([]OwnershipConcern, error) {
	wantUID := s.layout.HostUID
	var concerns []OwnershipConcern

	if dir := dockerConfigDirForAudit(s.layout); dir != "" {
		scan, err := fileutil.ScanWrongOwner(ctx, dir, wantUID, OwnershipMaxSample)
		if err != nil {
			return concerns, err
		}
		if scan.Count > 0 {
			concerns = append(concerns, OwnershipConcern{
				Label:       "Docker config",
				Root:        dir,
				ExpectedUID: wantUID,
				ExpectedGID: s.layout.HostGID,
				Count:       scan.Count,
				Sample:      scan.Sample,
			})
		}
	}

	return concerns, nil
}

// dockerConfigDirForAudit resolves the Docker config dir a build would use from
// the layout's curated env, mirroring the (unexported) resolver in
// runtime/docker so the audit inspects the same path. Empty when neither
// DOCKER_CONFIG nor HOME is set (nothing to inspect).
func dockerConfigDirForAudit(layout config.Layout) string {
	env := layout.Env().EnvForDaemonDiscovery()
	if d := env["DOCKER_CONFIG"]; d != "" {
		return d
	}
	if home := env["HOME"]; home != "" {
		return filepath.Join(home, ".docker")
	}
	return ""
}
