//go:build linux

package containerdrt

// ABOUTME: Prune orphaned containerd containers and CNI state from the yoloai namespace.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
)

// Prune removes orphaned containerd containers in the yoloai namespace.
// Any container named yoloai-* that is not in knownInstances is considered orphaned.
// For each removed container, CNI teardown is attempted.
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	ctx = r.withNamespace(ctx)

	known := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		known[name] = true
	}

	containers, err := r.client.Containers(ctx)
	if err != nil {
		return runtime.PruneResult{}, fmt.Errorf("list containers: %w", err)
	}

	var result runtime.PruneResult
	for _, ctr := range containers {
		name := ctr.ID()
		if !strings.HasPrefix(name, "yoloai-") {
			continue
		}
		if known[name] {
			continue
		}

		item := runtime.PruneItem{
			Kind: "container",
			Name: name,
		}

		if !dryRun {
			if err := r.Remove(ctx, name); err != nil {
				if !errors.Is(err, runtime.ErrNotFound) {
					fmt.Fprintf(output, "Warning: failed to remove container %s: %v\n", name, err) //nolint:errcheck // best-effort output
					continue
				}
				// Container already gone — treat as successful deletion.
			}
		}
		result.Items = append(result.Items, item)
	}

	return result, nil
}
