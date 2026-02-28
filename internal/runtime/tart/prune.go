package tart

// ABOUTME: Finds and removes orphaned yoloai-* Tart VMs.

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Prune implements runtime.Runtime.
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	known := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		known[name] = true
	}

	out, err := r.runTart(ctx, "list", "--quiet")
	if err != nil {
		return runtime.PruneResult{}, fmt.Errorf("list VMs: %w", err)
	}

	var result runtime.PruneResult
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || !strings.HasPrefix(name, "yoloai-") {
			continue
		}
		if known[name] {
			continue
		}

		result.Items = append(result.Items, runtime.PruneItem{
			Kind: "vm",
			Name: name,
		})

		if !dryRun {
			if _, err := r.runTart(ctx, "delete", name); err != nil {
				fmt.Fprintf(output, "Warning: failed to delete VM %s: %v\n", name, err) //nolint:errcheck // best-effort output
			}
		}
	}

	return result, nil
}
