package tart

// ABOUTME: VM provisioning for Tart: pulls base macOS image, installs dev tools.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/buildinfo"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

const (
	// defaultBaseImage is the Cirrus Labs macOS base image pulled on first run.
	defaultBaseImage = "ghcr.io/cirruslabs/macos-sequoia-base:latest"

	// provisionedImageName is the local VM name for the provisioned base image.
	provisionedImageName = "yoloai-base"
)

// provisionCommands are the shell commands to install dev tools in the base VM.
// Each entry is run via tart exec as the admin user. They install Homebrew,
// node@22, tmux, jq, ripgrep, and Claude Code, then compose a deterministic
// login-shell PATH.
//
// These commands are checksummed (see provisionChecksum): any edit invalidates
// the stored checksum and forces a rebuild. Verification and the build imprint
// are deliberately NOT part of this slice — the imprint embeds the checksum, so
// including it would be circular; they run as trailing dynamic steps instead.
//
// Notes:
//   - Claude Code is installed via the native installer; the npm package is
//     deprecated as of v2.1.15 and slated to stop working. The native installer
//     drops a standalone binary in ~/.local/bin and self-updates — no node
//     dependency, which removes the whole node-version-shadowing class of bugs
//     that the old npm-based install fought against.
//   - node@22 is a keg-only (versioned) formula, so brew does not link it into
//     /opt/homebrew/bin. We add its bin dir to the login PATH explicitly.
var provisionCommands = []string{
	// Install Homebrew if not present (non-interactive).
	`which brew >/dev/null 2>&1 || NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`,

	// Install pinned tools. node@22 is keg-only; its bin dir is added to the
	// login PATH below rather than relying on brew link.
	`eval "$(/opt/homebrew/bin/brew shellenv)" && brew install tmux jq ripgrep node@22`,

	// Install Claude Code via the native installer (standalone binary in
	// ~/.local/bin, self-updating, no node dependency).
	`curl -fsSL https://claude.ai/install.sh | bash`,

	// Compose a deterministic login-shell PATH: Homebrew, keg-only node@22, and
	// the native Claude install dir. Guarded by a marker so it is idempotent.
	`grep -q 'yoloai-base PATH' ~/.zprofile 2>/dev/null || printf '%s\n' '# yoloai-base PATH' 'eval "$(/opt/homebrew/bin/brew shellenv)"' 'export PATH="/opt/homebrew/opt/node@22/bin:$HOME/.local/bin:$PATH"' >> ~/.zprofile`,
}

// requiredTools are the binaries the provisioned base must expose on the login
// shell PATH. Verified in-guest after provisioning; a missing tool fails the
// build before the new base is promoted.
var requiredTools = []string{"tmux", "node", "jq", "rg", "claude"}

// Setup ensures the provisioned base VM image exists, pulling and provisioning
// as needed. If imageRef is set in config (tart.image override), it uses that
// as the base instead of the default.
//
// The layout parameter is currently unused by the tart Setup path — it's
// accepted to satisfy the runtime.Runtime interface (Q-W.5) and remains
// available for any future host-path needs (e.g., lock files) without a
// further signature change.
func (r *Runtime) Setup(ctx context.Context, _ config.Layout, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	baseImage := r.resolveBaseImage(sourceDir)

	// Serialize base creation so concurrent processes don't race on the shared
	// yoloai-base VM. Mirrors the Docker backend's Setup (Q-W.4a).
	release, err := AcquireBaseLock(r.layout, provisionedImageName)
	if err != nil {
		return err
	}
	defer release()

	// Re-check inside the lock: another process may have built the base while
	// we were blocked acquiring it.
	if !force {
		rebuild, err := r.needsBuild(ctx, baseImage)
		if err != nil {
			return fmt.Errorf("check base image: %w", err)
		}
		if !rebuild {
			return nil
		}
	}

	// Ensure the upstream base image is present.
	baseExists, err := r.vmExistsNamed(ctx, baseImage)
	if err != nil {
		return fmt.Errorf("check base image: %w", err)
	}
	if !baseExists {
		fmt.Fprintf(output, "Pulling base macOS VM image (%s)...\n", baseImage)            //nolint:errcheck // best-effort
		fmt.Fprintln(output, "This is a one-time download (~30 GB) and may take a while.") //nolint:errcheck // best-effort

		if err := r.pullImage(ctx, baseImage, output); err != nil {
			return fmt.Errorf("pull base image: %w", err)
		}
	}

	// Provision into a temp VM, then atomically swap it into place. The
	// existing yoloai-base (if any) stays intact and trusted until the new one
	// is fully provisioned and verified — so a crash mid-build can never leave
	// a trusted-but-empty base.
	tempVM := generateTempVMName(provisionedImageName)
	defer r.cleanupTempVM(ctx, tempVM)

	fmt.Fprintln(output, "Cloning base image for provisioning...") //nolint:errcheck // best-effort
	if _, err := r.runTart(ctx, "clone", baseImage, tempVM); err != nil {
		return fmt.Errorf("clone base image: %w", err)
	}

	fmt.Fprintln(output, "Booting VM for provisioning (installing dev tools)...") //nolint:errcheck // best-effort
	if err := r.bootForProvisioning(ctx, tempVM, baseImage, output, logger); err != nil {
		return fmt.Errorf("provision VM: %w", err)
	}

	// Swap: replace the old base with the freshly verified temp VM.
	provExists, _ := r.vmExistsNamed(ctx, provisionedImageName)
	if provExists {
		fmt.Fprintln(output, "Promoting newly provisioned image...") //nolint:errcheck // best-effort
		if _, err := r.runTart(ctx, "delete", provisionedImageName); err != nil {
			return fmt.Errorf("delete old base: %w", err)
		}
	}
	if _, err := r.runTart(ctx, "clone", tempVM, provisionedImageName); err != nil {
		return fmt.Errorf("promote provisioned image: %w", err)
	}

	// Record the checksum and host-side build info LAST — only after a verified
	// base is in place. This closes the decoupling gap: a stale checksum can
	// never bless a missing base (needsBuild sees the VM is absent and rebuilds).
	r.recordBuildChecksum(baseImage)
	r.recordBuildInfo(baseImage)

	fmt.Fprintln(output, "Base VM image provisioned successfully.") //nolint:errcheck // best-effort
	return nil
}

// IsReady returns true if the provisioned yoloai-base VM exists locally.
func (r *Runtime) IsReady(ctx context.Context) (bool, error) {
	return r.vmExistsNamed(ctx, provisionedImageName)
}

// vmExistsNamed checks if a Tart VM with the given name exists locally.
func (r *Runtime) vmExistsNamed(ctx context.Context, vmName string) (bool, error) {
	out, err := r.runTart(ctx, "list", "--quiet")
	if err != nil {
		return false, fmt.Errorf("list VMs: %w", err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == vmName {
			return true, nil
		}
	}
	return false, nil
}

// resolveBaseImage returns the base image to use, checking for a config
// override in tart.image.
func (r *Runtime) resolveBaseImage(_ string) string {
	if r.baseImageOverride != "" {
		return r.baseImageOverride
	}
	return defaultBaseImage
}

// tartBaseChecksumPath returns the path where the tart base image build
// checksum is stored under the given layout's cache directory.
func (r *Runtime) tartBaseChecksumPath() string {
	return filepath.Join(r.layout.CacheDir(), ".tart-base-checksum")
}

// provisionChecksum computes a SHA-256 of the provision commands and the base
// image name. Any change to either invalidates the stored checksum and triggers
// a rebuild.
func (r *Runtime) provisionChecksum(baseImage string) string {
	h := sha256.New()
	for _, cmd := range provisionCommands {
		h.Write([]byte(cmd))
	}
	h.Write([]byte(baseImage))
	return hex.EncodeToString(h.Sum(nil))
}

// needsBuild returns true when the provisioned VM image is absent or was built
// from different inputs (commands or base image changed).
func (r *Runtime) needsBuild(ctx context.Context, baseImage string) (bool, error) {
	exists, err := r.vmExistsNamed(ctx, provisionedImageName)
	if err != nil {
		return true, err
	}
	if !exists {
		return true, nil
	}
	current := r.provisionChecksum(baseImage)
	last, err := os.ReadFile(r.tartBaseChecksumPath()) //nolint:gosec // G304: path is DataDir/cache/
	if err != nil {
		return true, nil //nolint:nilerr // no record → rebuild; read error is expected on first run
	}
	return string(last) != current, nil
}

// recordBuildChecksum persists the current provision checksum so future
// needsBuild calls can skip an unnecessary rebuild.
func (r *Runtime) recordBuildChecksum(baseImage string) {
	sum := r.provisionChecksum(baseImage)
	_ = fileutil.WriteFile(r.tartBaseChecksumPath(), []byte(sum), 0600) //nolint:gosec // G304: path is DataDir/cache/
}

// tartBaseInfoPath returns the path of the host-side build-info sidecar, next
// to the checksum file under the layout's cache directory.
func (r *Runtime) tartBaseInfoPath() string {
	return filepath.Join(r.layout.CacheDir(), ".tart-base-info")
}

// buildInfoContent renders the build imprint: the yoloai build that produced
// the base, the provision checksum, the base image, and a UTC build timestamp.
// Shared by the in-guest imprint and the host-side sidecar so they agree.
func (r *Runtime) buildInfoContent(baseImage string) string {
	return fmt.Sprintf(
		"yoloai_version=%s\nyoloai_commit=%s\nyoloai_build_date=%s\nprovision_checksum=%s\nbase_image=%s\nbuilt_at=%s\n",
		buildinfo.Version, buildinfo.Commit, buildinfo.Date,
		r.provisionChecksum(baseImage), baseImage,
		time.Now().UTC().Format(time.RFC3339),
	)
}

// recordBuildInfo writes the host-side build-info sidecar (best-effort).
func (r *Runtime) recordBuildInfo(baseImage string) {
	_ = fileutil.WriteFile(r.tartBaseInfoPath(), []byte(r.buildInfoContent(baseImage)), 0600) //nolint:gosec // G304: path is DataDir/cache/
}

// verifyTools asserts every requiredTools binary resolves on the VM's login
// shell PATH (zsh -l sources ~/.zprofile). Returns an error naming the first
// missing tool — that is what the provisioned base must guarantee.
func (r *Runtime) verifyTools(ctx context.Context, vmName string, output io.Writer) error {
	script := fmt.Sprintf(
		`for t in %s; do command -v "$t" >/dev/null 2>&1 || { echo "MISSING: $t" >&2; exit 1; }; done`,
		strings.Join(requiredTools, " "),
	)
	args := execArgs(vmName, "zsh", "-lc", script)
	cmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tool verification failed (a required tool of %v is missing from the login PATH): %w", requiredTools, err)
	}
	return nil
}

// writeImprint writes the build-info file inside the guest at ~/.yoloai-base-info.
// The content is piped via stdin to avoid shell-quoting the multi-line payload.
func (r *Runtime) writeImprint(ctx context.Context, vmName, baseImage string) error {
	args := execArgs(vmName, "bash", "-c", "cat > ~/.yoloai-base-info")
	cmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204
	cmd.Stdin = strings.NewReader(r.buildInfoContent(baseImage))
	return cmd.Run()
}

// pullImage pulls a Tart VM image from a registry.
func (r *Runtime) pullImage(ctx context.Context, imageRef string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, r.tartBin, "pull", imageRef) //nolint:gosec // G204: imageRef from config
	cmd.Stdout = output
	cmd.Stderr = output
	return cmd.Run()
}

// bootForProvisioning boots a VM, runs provision commands, verifies the
// required tools resolve on the login PATH, writes a build imprint, then shuts
// the VM down. baseImage is needed for the imprint's provision checksum.
func (r *Runtime) bootForProvisioning(ctx context.Context, vmName, baseImage string, output io.Writer, logger *slog.Logger) error {
	// Capture tart run output to a temp log for debugging
	vmLog, err := os.CreateTemp("", "yoloai-tart-*.log")
	if err != nil {
		return fmt.Errorf("create VM log: %w", err)
	}
	vmLogPath := vmLog.Name()
	defer os.Remove(vmLogPath) //nolint:errcheck // best-effort cleanup

	// Start the VM in the background for provisioning
	cmd := exec.CommandContext(ctx, r.tartBin, "run", "--no-graphics", vmName) //nolint:gosec // G204
	cmd.Stdout = vmLog
	cmd.Stderr = vmLog
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		_ = vmLog.Close()
		return fmt.Errorf("start VM for provisioning: %w", err)
	}

	// Monitor the tart run process so we can detect early exits
	procDone := make(chan error, 1)
	go func() {
		procDone <- cmd.Wait()
		_ = vmLog.Close()
	}()

	// Safety net: if we return early (boot failure, provision error), stop the VM.
	// On the success path the VM will already be stopped via in-guest shutdown
	// before we return, so tart stop will be a no-op or benign failure.
	defer func() {
		logger.Debug("stopping provisioning VM (safety net)", "vm", vmName)
		stopCmd := exec.CommandContext(ctx, r.tartBin, "stop", vmName) //nolint:gosec // G204
		if err := stopCmd.Run(); err != nil {
			// Fall back to killing the tart run host process
			_ = cmd.Process.Kill()
		}
	}()

	// Wait for VM to be accessible
	fmt.Fprintln(output, "Waiting for VM to boot (macOS VMs can take 30-60s)...") //nolint:errcheck // best-effort
	if err := r.waitForBoot(ctx, vmName, procDone); err != nil {
		// Show tart run output on failure to aid debugging
		if logData, readErr := os.ReadFile(vmLogPath); readErr == nil && len(logData) > 0 { //nolint:gosec // G304: temp file we created
			// The most common boot failure is Apple's concurrent-VM cap: a base
			// build needs to run a VM, but two macOS VMs may already be running.
			// Surface the actionable "stop a sandbox" guidance instead of a raw log.
			if limitErr := checkVMLimitError(string(logData)); limitErr != nil {
				return limitErr
			}
			fmt.Fprintf(output, "tart run output:\n%s\n", string(logData)) //nolint:errcheck,gosec // best-effort diagnostic output
		}
		return fmt.Errorf("vm did not become accessible: %w", err)
	}

	// Run each provision command
	for i, cmdStr := range provisionCommands {
		fmt.Fprintf(output, "Provisioning step %d/%d...\n", i+1, len(provisionCommands)) //nolint:errcheck // best-effort
		logger.Debug("provisioning", "step", i+1, "command", cmdStr)

		args := execArgs(vmName, "bash", "-c", cmdStr)
		provCmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204
		provCmd.Stdout = output
		provCmd.Stderr = output

		if err := provCmd.Run(); err != nil {
			return fmt.Errorf("provision step %d failed: %w", i+1, err)
		}
	}

	// Verify every required tool resolves on the LOGIN shell PATH (zsh -l
	// sources ~/.zprofile) — that is the PATH the agent and tmux pane will use.
	// A missing tool fails the build here, before the new base is promoted.
	fmt.Fprintln(output, "Verifying provisioned tools...") //nolint:errcheck // best-effort
	if err := r.verifyTools(ctx, vmName, output); err != nil {
		return err
	}

	// Write the build imprint: which yoloai build produced this base, plus the
	// provision checksum and a UTC timestamp. Metadata only — not a rebuild
	// trigger (the checksum drives rebuilds; embedding it here would be
	// circular if it were part of provisionCommands).
	fmt.Fprintln(output, "Writing base image imprint...") //nolint:errcheck // best-effort
	if err := r.writeImprint(ctx, vmName, baseImage); err != nil {
		return fmt.Errorf("write imprint: %w", err)
	}

	// Trigger an in-guest shutdown to flush all APFS write buffers before the
	// disk image is cloned for sandboxes. Without this, an external tart stop
	// (ACPI power-off) may not wait for macOS to commit all pending writes,
	// causing installed packages and profile edits to be missing in clones.
	fmt.Fprintln(output, "Flushing filesystem and shutting down provisioning VM...") //nolint:errcheck // best-effort
	shutArgs := execArgs(vmName, "bash", "-c", "sync; sudo /sbin/shutdown -h now")
	shutCmd := exec.CommandContext(ctx, r.tartBin, shutArgs...) //nolint:gosec // G204
	shutCmd.Stdout = output
	shutCmd.Stderr = output
	_ = shutCmd.Run() // VM shuts down during exec — non-zero exit is expected

	// Wait for tart run to exit, confirming the VM has fully powered off and
	// its disk image is in a consistent state.
	fmt.Fprintln(output, "Waiting for VM to fully power off...") //nolint:errcheck // best-effort
	select {
	case <-procDone:
		logger.Debug("provisioning VM powered off cleanly", "vm", vmName)
	case <-time.After(90 * time.Second):
		return fmt.Errorf("provisioning VM did not power off within 90s")
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}
