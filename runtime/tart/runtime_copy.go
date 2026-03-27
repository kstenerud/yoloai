package tart

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// CopyRuntimeToVM downloads and installs a runtime using xcodebuild -downloadPlatform.
// This approach is verified to work correctly (see docs/dev/research/ios-runtime-download-verification.md).
// The ditto copy approach produced incomplete runtimes that failed to boot simulators.
// VM must be running with Xcode configured.
func CopyRuntimeToVM(ctx context.Context, vmName string, runtime RuntimeVersion) error {
	// Capitalize platform for xcodebuild (iOS, tvOS, watchOS, visionOS)
	platformCap := CapitalizePlatform(runtime.Platform)

	// Use xcodebuild to download the runtime (downloads latest for the platform)
	// Note: xcodebuild -downloadPlatform doesn't support specific version selection;
	// it always downloads the latest available. The runtime is resolved on the host
	// before this function is called, so we know what version should be available.
	fmt.Printf("Downloading %s %s runtime...\n", platformCap, runtime.Version)
	downloadCmd := fmt.Sprintf("xcodebuild -downloadPlatform %s", platformCap)
	args := execArgs(vmName, "bash", "-c", downloadCmd)
	cmd := exec.CommandContext(ctx, "tart", args...) //nolint:gosec // G204: vmName from validated state

	// Stream stdout and stderr to show download progress
	// xcodebuild outputs progress updates with carriage returns (\r)
	// The terminal will handle the updates automatically
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download runtime: %w", err)
	}

	// Verify runtime is recognized by simctl
	fmt.Printf("Verifying runtime...\n")
	verifyCmd := fmt.Sprintf("xcrun simctl list runtimes 2>&1 | grep '%s %s'",
		platformCap, runtime.Version)
	args = execArgs(vmName, "bash", "-c", verifyCmd)
	cmd = exec.CommandContext(ctx, "tart", args...) //nolint:gosec // G204: vmName from validated state
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("verify runtime: %w: %s", err, stderr.String())
		}
		return fmt.Errorf("verify runtime: %w", err)
	}

	fmt.Printf("Runtime verified successfully\n")
	return nil
}
