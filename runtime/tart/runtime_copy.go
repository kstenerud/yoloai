package tart

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// CopyRuntimeToVM copies a runtime bundle from host VirtioFS mount to VM local storage
// VM must be running and runtime directory must be mounted via VirtioFS
func CopyRuntimeToVM(ctx context.Context, vmName string, runtime RuntimeVersion) error {
	// Find runtime bundle path on host
	bundlePath, err := getRuntimeBundlePath(runtime)
	if err != nil {
		return fmt.Errorf("get runtime bundle path: %w", err)
	}

	// Extract runtime name from bundle path (e.g., "iOS 26.2.simruntime")
	runtimeName := filepath.Base(bundlePath)

	// VirtioFS path in VM (mounted from /Library/Developer/CoreSimulator/Volumes/)
	// Assumes existing mount logic in tart.go already mounts this directory
	virtiofsSrcPath := "/Volumes/My Shared Files/m-Volumes/" + strings.TrimPrefix(bundlePath, "/Library/Developer/CoreSimulator/Volumes/")
	vmDstPath := "/Library/Developer/CoreSimulator/Profiles/Runtimes/"

	// Create target directory
	cmd := exec.CommandContext(ctx, "tart", "exec", vmName, "--", //nolint:gosec // G204: vmName is from validated VM state
		"sudo", "mkdir", "-p", vmDstPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}

	// Copy runtime bundle using ditto (preserves metadata)
	cmd = exec.CommandContext(ctx, "tart", "exec", vmName, "--", //nolint:gosec // G204: vmName is from validated VM state
		"sudo", "ditto", virtiofsSrcPath, vmDstPath+runtimeName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy runtime bundle: %w", err)
	}

	// Fix Info.plist (ditto may fail on this due to permissions)
	cmd = exec.CommandContext(ctx, "tart", "exec", vmName, "--", //nolint:gosec // G204: vmName is from validated VM state
		"sudo", "cp",
		virtiofsSrcPath+"/Contents/Info.plist",
		vmDstPath+runtimeName+"/Contents/Info.plist")
	_ = cmd.Run() // Best effort

	// Verify runtime is visible
	// Capitalize first letter of platform (ios -> iOS, tvos -> tvOS, etc.)
	platformCap := strings.ToUpper(runtime.Platform[:1]) + runtime.Platform[1:]
	verifyCmd := fmt.Sprintf("xcrun simctl list runtimes | grep '%s %s'",
		platformCap, runtime.Version)
	cmd = exec.CommandContext(ctx, "tart", "exec", vmName, "--", //nolint:gosec // G204: vmName and verifyCmd are from validated state
		"bash", "-c", verifyCmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("runtime not visible after copy (verify failed): %w", err)
	}

	return nil
}

// getRuntimeBundlePath queries simctl for the bundle path of a runtime
func getRuntimeBundlePath(runtime RuntimeVersion) (string, error) {
	cmd := exec.Command("xcrun", "simctl", "list", "runtimes", "--json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("query simctl: %w", err)
	}

	var result simctlRuntimesOutput
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("parse simctl output: %w", err)
	}

	for _, rt := range result.Runtimes {
		if strings.ToLower(rt.Platform) == runtime.Platform && rt.Version == runtime.Version {
			return rt.BundlePath, nil
		}
	}

	return "", fmt.Errorf("runtime %s %s not found", runtime.Platform, runtime.Version)
}
