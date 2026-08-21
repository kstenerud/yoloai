// ABOUTME: CopyRuntimeToVM installs an Apple simulator runtime into a Tart VM
// ABOUTME: via xcodebuild -downloadPlatform, plus simctl verification.
package tart

import (
	"bytes"
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// CopyRuntimeToVM downloads and installs a runtime using xcodebuild -downloadPlatform.
// This approach is verified to work correctly (see docs/contributors/design/research/ios-runtime-download-verification.md).
// The ditto copy approach produced incomplete runtimes that failed to boot simulators.
// VM must be running with Xcode configured.
// env is the explicit subprocess environment (DEV §12); pass r.execEnv from the Runtime.
// Progress is emitted as records; the library never touches the process's
// os.Stdout/Stderr (§12).
func CopyRuntimeToVM(ctx context.Context, env []string, tartBin, vmName string, runtime RuntimeVersion, progress feedback.ProgressSink) error {
	// Capitalize platform for xcodebuild (iOS, tvOS, watchOS, visionOS)
	platformCap := CapitalizePlatform(runtime.Platform)

	// Use xcodebuild to download the runtime (downloads latest for the platform)
	// Note: xcodebuild -downloadPlatform doesn't support specific version selection;
	// it always downloads the latest available. The runtime is resolved on the host
	// before this function is called, so we know what version should be available.
	feedback.EmitProgress(progress, feedback.Progress{
		Event:   "runtime.downloading",
		Message: fmt.Sprintf("Downloading %s %s runtime...", platformCap, runtime.Version),
		Fields:  map[string]any{"platform": runtime.Platform, "version": runtime.Version},
	})
	downloadCmd := fmt.Sprintf("xcodebuild -downloadPlatform %s", platformCap)
	args := execArgs(vmName, "bash", "-c", downloadCmd)
	cmd := sysexec.CommandContext(ctx, env, tartBin, args...)

	// xcodebuild reports download progress as \r-terminated updates with no
	// newline until the end. ProgressWriter splits on \r as well as \n for
	// exactly this, so each update is its own record — a terminal consumer can
	// redraw in place, and anything else can sample or drop them. Handing it a
	// raw writer instead would buffer a multi-GB download into one line.
	pw := feedback.NewProgressWriter(progress, "runtime.download_output")
	defer pw.Flush()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download runtime: %w", err)
	}

	// Verify runtime is recognized by simctl
	feedback.Progressf(progress, "runtime.verifying", "Verifying runtime...")
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

	feedback.Progressf(progress, "runtime.verified", "Runtime verified successfully")
	return nil
}
