// ABOUTME: ExecuteVMWorkDirSetup: VM-side work directory initialisation for
// ABOUTME: backends that implement WorkDirSetup (e.g., Tart).
package launch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// firstlaunchStormCeiling bounds how long a VM work-dir setup command is
// re-probed while it fails with the Tart `xcodebuild -runFirstLaunch`
// security-scan storm signature. The storm lasts as long as firstlaunch runs
// (60-120s+), so we probe to a ceiling rather than guess its duration — the
// same strategy the tmux resolver uses (see
// docs/contributors/backend-idiosyncrasies.md). Overridable in tests.
var (
	firstlaunchStormCeiling = 240 * time.Second
	stormRetryInterval      = 1 * time.Second
)

// ExecuteVMWorkDirSetup runs VM-side work directory setup for backends that
// implement WorkDirSetup (e.g., Tart). For every :copy dir in the environment
// (workdir and any :copy aux dirs), it copies from VirtioFS staging to local VM
// storage, creates the git baseline inside the VM, retrieves the baseline SHA,
// and writes it back to the environment. Saves environment.json once after all
// dirs are processed. Returns nil if the runtime does not implement WorkDirSetup
// (Docker/containerd).
func ExecuteVMWorkDirSetup(ctx context.Context, rt runtime.Backend, name, sandboxDir string, meta *store.Environment) error {
	// Only SandboxSide backends keep the work copy inside the sandbox and need
	// VM-side setup; HostSide backends (Docker/containerd) baseline on the host.
	if runtime.LocalityOf(rt) != runtime.LocalitySandboxSide {
		return nil
	}
	setupIntf, ok := rt.(runtime.WorkDirSetup)
	if !ok {
		return fmt.Errorf("yoloai bug: backend %s declares SandboxSide filesystem locality but does not implement WorkDirSetup", rt.Descriptor().Type)
	}

	instance := store.InstanceName(meta.Principal, name)

	for i := range meta.Dirs {
		if meta.Dirs[i].Mode != store.DirModeCopy {
			continue
		}
		if err := setupVMCopyDir(ctx, rt, setupIntf, instance, name, meta, i); err != nil {
			return err
		}
	}

	return store.SaveEnvironment(sandboxDir, meta)
}

// setupVMCopyDir runs the VM-side setup for a single :copy directory at index i
// in meta.Dirs: copies from VirtioFS staging, baselines, and records the SHA.
func setupVMCopyDir(ctx context.Context, rt runtime.Backend, setupIntf runtime.WorkDirSetup, instance, name string, meta *store.Environment, i int) error {
	vfsPath := filepath.Join("/Volumes/My Shared Files/yoloai/work", config.EncodePath(meta.Dirs[i].HostPath))
	vmLocalPath := runtime.ResolveCopyMountFor(rt, name, meta.Dirs[i].HostPath)

	cmds := setupIntf.SetupWorkDirInVM(vfsPath, vmLocalPath)
	for _, cmd := range cmds {
		_, err := execVMSetupWithStormRetry(ctx, func() (runtime.ExecResult, error) {
			return rt.Exec(ctx, instance, []string{"bash", "-c", cmd}, "admin")
		})
		if err != nil {
			return fmt.Errorf("setup work dir in VM: %w", err)
		}
	}

	// Retrieve baseline SHA
	result, err := execVMSetupWithStormRetry(ctx, func() (runtime.ExecResult, error) {
		return rt.Exec(ctx, instance, []string{"git", "-C", vmLocalPath, "rev-parse", "HEAD"}, "admin")
	})
	if err != nil {
		return fmt.Errorf("get baseline SHA: %w", err)
	}

	meta.Dirs[i].BaselineSHA = strings.TrimSpace(result.Stdout)
	if meta.Dirs[i].InceptionSHA == "" {
		meta.Dirs[i].InceptionSHA = meta.Dirs[i].BaselineSHA
	}
	return nil
}

// execVMSetupWithStormRetry runs a single VM-setup command, retrying while it
// fails with the firstlaunch security-scan storm's transient signature
// (see isFirstlaunchStormTransient) until it succeeds, hits a non-transient
// error, the storm ceiling elapses, or the context is cancelled. The happy path
// runs once and never sleeps.
func execVMSetupWithStormRetry(ctx context.Context, run func() (runtime.ExecResult, error)) (runtime.ExecResult, error) {
	deadline := time.Now().Add(firstlaunchStormCeiling)
	for {
		result, err := run()
		if err == nil || !isFirstlaunchStormTransient(err) {
			return result, err
		}
		if !time.Now().Before(deadline) {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(stormRetryInterval):
		}
	}
}

// isFirstlaunchStormTransient reports whether err is the transient failure
// produced by the Tart `xcodebuild -runFirstLaunch` security-scan storm. While
// firstlaunch runs in the background, two host-observable symptoms appear and
// then clear once it subsides: the Xcode license check fails (git/xcodebuild
// exit 69, since git on macOS is the xcode-select shim) and tooling briefly
// vanishes from PATH (exit 127, command not found). See the "transient FS/PATH
// failure" entry in docs/contributors/backend-idiosyncrasies.md.
func isFirstlaunchStormTransient(err error) bool {
	var execErr *runtime.ExecError
	if !errors.As(err, &execErr) {
		return false
	}
	switch {
	case execErr.ExitCode == 69 && strings.Contains(execErr.Stderr, "Xcode license"):
		return true
	case execErr.ExitCode == 127:
		return true
	default:
		return false
	}
}
