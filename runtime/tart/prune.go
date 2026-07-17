package tart

// ABOUTME: Finds and removes orphaned yoloai-* Tart VMs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// testPrincipalVMRe matches the leading segment of a VM owned by a test
// principal — testutil.UniqueTestPrincipal mints "tNNNNNNN" (config.MaxPrincipalLength
// is 8), and those VMs are real on a developer's machine, not hypothetical.
var testPrincipalVMRe = regexp.MustCompile(`^t[0-9]{7}-`)

// legacyCLIVMName reports whether a VM name is a pre-D126 CLI instance
// ("yoloai-<sandbox>", before the CLI adopted the "cli" principal), so the CLI's
// sweep can still reclaim it (DF125). Without this, a VM whose sandbox dir is
// already gone keeps its legacy name, no longer matches "yoloai-cli-", and holds
// one of the host's capped VM slots forever with no yoloai command able to name
// it.
//
// Tart is the ONLY backend that needs this, and the only one where it is a
// heuristic rather than a fact: it stores no labels (DF124 — for a label-less
// backend the name is the identity), so unlike the container backends there is
// nothing recorded to read, and the name alone cannot decide it. The legacy form
// `yoloai-<S>` overlaps every principal namespace, because a SandboxName may
// contain '-' where a PrincipalSegment may not: `yoloai-acme-probe` is both the
// legacy VM of a sandbox named "acme-probe" and principal "acme"'s sandbox
// "probe" (DF125). So this deliberately matches only what has ACTUALLY existed —
// the CLI ("") and the test principals — rather than pretending to be exact:
// excluding the two namespaces that have ever been minted leaves the legacy CLI.
//
// It over-reaches only for a principal that has never existed: an integrator
// running tart under their own principal, sharing this host, whose sandbox names
// collide. If that ever ships, this must go. It is therefore the one part of the
// DF125 fix with an expiry — but the expiry is a settling period, NOT a release:
// a user may upgrade 0.8.0 -> 0.10.0 directly and still hold legacy VMs, so
// dropping it "next release" would abandon exactly the people it exists for.
// Retire it once legacy VMs are gone in practice, not on a version schedule.
//
// Gated on the CLI's principal for the same reason as the label path: the
// unprincipaled namespace was the CLI's alone (DF115).
func legacyCLIVMName(name string, principal config.PrincipalSegment) bool {
	if principal != config.CLIPrincipal {
		return false
	}
	rest, ok := strings.CutPrefix(name, "yoloai-")
	if !ok {
		return false
	}
	// A principal-namespaced VM is "yoloai-<principal>-<sandbox>". Exclude the
	// namespaces that exist so what remains is the elided (legacy CLI) form.
	if strings.HasPrefix(rest, string(config.CLIPrincipal)+"-") || testPrincipalVMRe.MatchString(rest) {
		return false
	}
	return true
}

// Prune implements runtime.Backend.
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	known := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		known[name] = true
	}

	out, err := r.runTart(ctx, "list", "--quiet")
	if err != nil {
		return runtime.PruneResult{}, fmt.Errorf("list VMs: %w", err)
	}

	// Scope the sweep to this runtime's principal so a test or secondary
	// principal never reclaims VMs owned by a different principal (DF19).
	prefix := config.InstancePrefix(r.layout.Principal)

	var result runtime.PruneResult
	for line := range strings.SplitSeq(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || (!strings.HasPrefix(name, prefix) && !legacyCLIVMName(name, r.layout.Principal)) {
			continue
		}
		// The provisioned base template shares the yoloai- prefix and the
		// tart-list VM namespace with sandboxes, but it is not an orphan — it
		// is the reusable image every `new` clones from. Reclaiming it is the
		// job of PruneCache (`--images`), never the orphan sweep.
		if name == provisionedImageName {
			continue
		}
		if known[name] {
			continue
		}

		item := runtime.PruneItem{
			Kind: "vm",
			Name: name,
		}

		if !dryRun {
			// Stop the VM before deleting — tart delete fails on running VMs.
			r.stopVM(ctx, name)
			if _, err := r.runTart(ctx, "delete", name); err != nil {
				if !errors.Is(err, runtime.ErrNotFound) {
					fmt.Fprintf(output, "Warning: failed to delete VM %s: %v\n", name, err) //nolint:errcheck // best-effort output
					continue
				}
				// VM already gone — treat as successful deletion.
			}
		}
		result.Items = append(result.Items, item)
	}

	return result, nil
}

// PruneCache implements runtime.CachePruner for tart. Deletes the provisioned
// yoloai-base VM and every OCI row for the pulled base image (both the tag and
// the digest row — see ownedImageRefs), then drops the build-checksum marker so
// the next sandbox creation re-pulls and re-provisions from scratch.
//
// Tart has no regenerable build cache distinct from the base image, so when
// includeImages is false (plain `prune`) there is nothing to reclaim without
// forcing a re-pull — this is a no-op. With includeImages true (`prune
// --images`) it removes the multi-GB base image: a "host dedicated to yoloai"
// operation. Running sandboxes are unaffected — they are independent clones,
// not references to these images.
//
// Returns bytes reclaimed, measured as the drop in this backend's own
// CacheUsage across the prune (before − after), the same self-attributed delta
// docker/podman use (working-notes D37). tart's `list` Size is whole-GB, so the
// figure is coarse but reconciles with what `system disk` reports.
func (r *Runtime) PruneCache(ctx context.Context, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if !includeImages {
		return 0, nil
	}

	before := r.ownedImageBytes(ctx)
	refs := r.ownedImageRefs(ctx)

	if dryRun {
		for _, name := range refs {
			fmt.Fprintf(output, "tart: would remove cached image %s\n", name) //nolint:errcheck // best-effort output
		}
		if before < 0 {
			before = 0
		}
		return before, nil
	}

	for _, name := range refs {
		// delete fails on a running VM; stop first (no-op for OCI images).
		r.stopVM(ctx, name)
		if _, err := r.runTart(ctx, "delete", name); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			fmt.Fprintf(output, "tart: failed to remove cached image %s: %v\n", name, err) //nolint:errcheck // best-effort output
			continue
		}
		fmt.Fprintf(output, "tart: removed cached image %s\n", name) //nolint:errcheck // best-effort output
	}

	// Drop the provision checksum so needsBuild re-provisions cleanly even
	// if a future base happens to hash identically.
	_ = os.Remove(r.tartBaseChecksumPath())

	reclaimed := int64(0)
	if after := r.ownedImageBytes(ctx); before >= 0 && after >= 0 && before > after {
		reclaimed = before - after
	}
	return reclaimed, nil
}
