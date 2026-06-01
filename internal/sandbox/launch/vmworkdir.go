// ABOUTME: ExecuteVMWorkDirSetup: VM-side work directory initialisation for
// ABOUTME: backends that implement WorkDirSetup (e.g., Tart).
package launch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// ExecuteVMWorkDirSetup runs VM-side work directory setup for backends that
// implement WorkDirSetup (e.g., Tart). It copies the work directory from
// VirtioFS staging to local VM storage, creates the git baseline inside the VM,
// retrieves the baseline SHA, and updates environment.json with the SHA.
// Returns nil if the runtime does not implement WorkDirSetup (Docker/containerd).
func ExecuteVMWorkDirSetup(ctx context.Context, rt runtime.Runtime, name, sandboxDir string, meta *store.Environment) error {
	setupIntf, ok := rt.(runtime.WorkDirSetup)
	if !ok {
		return nil // Docker/containerd - no VM setup needed
	}

	vfsPath := filepath.Join("/Volumes/My Shared Files/yoloai/work", config.EncodePath(meta.Workdir.HostPath))
	vmLocalPath := runtime.ResolveCopyMountFor(rt, name, meta.Workdir.HostPath)

	cmds := setupIntf.SetupWorkDirInVM(vfsPath, vmLocalPath)
	for _, cmd := range cmds {
		_, err := rt.Exec(ctx, store.InstanceName(name), []string{"bash", "-c", cmd}, "admin")
		if err != nil {
			return fmt.Errorf("setup work dir in VM: %w", err)
		}
	}

	// Retrieve baseline SHA
	result, err := rt.Exec(ctx, store.InstanceName(name),
		[]string{"git", "-C", vmLocalPath, "rev-parse", "HEAD"}, "admin")
	if err != nil {
		return fmt.Errorf("get baseline SHA: %w", err)
	}

	// Update environment.json
	meta.Workdir.BaselineSHA = strings.TrimSpace(result.Stdout)
	if meta.Workdir.InceptionSHA == "" {
		meta.Workdir.InceptionSHA = meta.Workdir.BaselineSHA
	}
	return store.SaveEnvironment(sandboxDir, meta)
}
