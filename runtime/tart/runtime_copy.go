// ABOUTME: CopyRuntimeToVM installs an Apple simulator runtime into a Tart VM
// ABOUTME: via xcodebuild -downloadPlatform, plus simctl verification.
package tart

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// CopyRuntimeToVM downloads and installs a runtime using xcodebuild -downloadPlatform.
// This approach is verified to work correctly (see docs/contributors/design/research/ios-runtime-download-verification.md).
// The ditto copy approach produced incomplete runtimes that failed to boot simulators.
// VM must be running with Xcode configured.
// env is the explicit subprocess environment (DEV §12); pass r.execEnv from the Runtime.
// Progress is written to progress (the caller's writer); the library never
// touches the process's os.Stdout/Stderr (§12).
func CopyRuntimeToVM(ctx context.Context, env []string, tartBin, vmName string, runtime RuntimeVersion, progress io.Writer) error {
	// Capitalize platform for xcodebuild (iOS, tvOS, watchOS, visionOS)
	platformCap := CapitalizePlatform(runtime.Platform)

	// Use xcodebuild to download the runtime (downloads latest for the platform)
	// Note: xcodebuild -downloadPlatform doesn't support specific version selection;
	// it always downloads the latest available. The runtime is resolved on the host
	// before this function is called, so we know what version should be available.
	fmt.Fprintf(progress, "Downloading %s %s runtime...\n", platformCap, runtime.Version) //nolint:errcheck // best-effort progress
	downloadCmd := fmt.Sprintf("xcodebuild -downloadPlatform %s", platformCap)
	args := execArgs(vmName, "bash", "-c", downloadCmd)
	cmd := sysexec.CommandContext(ctx, env, tartBin, args...)

	// Stream the subprocess's live progress to the caller's writer.
	// xcodebuild outputs progress updates with carriage returns (\r); a TTY
	// writer animates them, a non-TTY one just scrolls.
	cmd.Stdout = progress
	cmd.Stderr = progress

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download runtime: %w", err)
	}

	// Verify runtime is recognized by simctl
	fmt.Fprintf(progress, "Verifying runtime...\n") //nolint:errcheck // best-effort progress
	verifyCmd := fmt.Sprintf("xcrun simctl list runtimes 2>&1 | grep '%s %s'",
		platformCap, runtime.Version)
	args = execArgs(vmName, "bash", "-c", verifyCmd)
	cmd = sysexec.CommandContext(ctx, env, tartBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("verify runtime: %w: %s", err, stderr.String())
		}
		return fmt.Errorf("verify runtime: %w", err)
	}

	fmt.Fprintf(progress, "Runtime verified successfully\n") //nolint:errcheck // best-effort progress
	return nil
}
