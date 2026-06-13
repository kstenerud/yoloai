package tart

// ABOUTME: VM provisioning for Tart: pulls base macOS image, installs dev tools.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/buildinfo"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

const (
	// newestKnownCodename is the most recent macOS the codename table knows. It
	// is the fallback when the host's macOS major isn't mapped (an OS newer or
	// older than anything in macOSCodenames) — pick the newest base we know
	// rather than guessing. A user on an unmapped host can always pin tart.image.
	newestKnownCodename = "tahoe"

	// defaultBaseImage is the host-match fallback: the newest base yoloai knows.
	// The actual default is host-matched (see resolveBaseImage); this constant is
	// the image used when the host major is unmapped.
	defaultBaseImage = "ghcr.io/cirruslabs/macos-" + newestKnownCodename + "-base:latest"

	// provisionedImageName is the local VM name for the provisioned base image.
	provisionedImageName = "yoloai-base"
)

// macOSCodenames maps a macOS major version to the Cirrus Labs image codename.
// Cirrus publishes one repo per macOS major (macos-<codename>-base) with no
// rolling "latest macOS" tag, so the major→codename mapping must live here.
// Extend it when Apple ships a new major and Cirrus publishes its base image;
// until then, an unmapped host falls back to newestKnownCodename and users can
// pin tart.image to the new repo the day it appears (no yoloai release needed).
var macOSCodenames = map[int]string{
	14: "sonoma",
	15: "sequoia",
	26: "tahoe",
}

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
	// login PATH below rather than relying on brew link. HOMEBREW_NO_AUTO_UPDATE
	// skips the formula-index refetch that otherwise runs on every brew install;
	// the base is rebuilt wholesale when provisionCommands change, so a stale
	// index never persists.
	`eval "$(/opt/homebrew/bin/brew shellenv)" && HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ENV_HINTS=1 brew install tmux jq ripgrep node@22`,

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
// accepted to satisfy the runtime.Backend interface (Q-W.5) and remains
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
		// Stop the old base before deleting it: `tart delete` on a running VM
		// fails with a misleading "instance not found", which would abandon a
		// fully-provisioned (and possibly hour-long) build at the final step.
		// stopVM is best-effort and a no-op when the VM is already stopped.
		r.stopVM(ctx, provisionedImageName)
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

// resolveBaseImage returns the base image to use. A tart.image config override
// always wins; otherwise the default is matched to the host's macOS major so
// the guest is new enough to run the host's Xcode (which yoloai mounts into the
// VM). The guest tracks host OS upgrades automatically — the developer already
// keeps the host current for App Store submission, so no yoloai release is
// needed to follow a new macOS. The provision checksum hashes this string, so a
// changed resolution triggers a rebuild on the next setup.
func (r *Runtime) resolveBaseImage(_ string) string {
	if r.baseImageOverride != "" {
		return r.baseImageOverride
	}
	// r.hostMajor is always set by New() (a closure over r.execEnv). Tests that
	// override it replace the entire closure, preserving the seam contract.
	if r.hostMajor == nil {
		return defaultBaseImage
	}
	return hostMatchedBaseImage(r.hostMajor)
}

// hostMatchedBaseImage builds the Cirrus base-image reference for the host's
// macOS major, falling back to newestKnownCodename when the major can't be
// determined or isn't mapped.
func hostMatchedBaseImage(hostMajor func() (int, error)) string {
	codename := newestKnownCodename
	if major, err := hostMajor(); err == nil {
		if name, ok := macOSCodenames[major]; ok {
			codename = name
		}
	}
	return fmt.Sprintf("ghcr.io/cirruslabs/macos-%s-base:latest", codename)
}

// hostMacOSMajor returns the host's macOS major version via `sw_vers
// -productVersion` (e.g. "26.1" → 26). The tart backend only runs on macOS, so
// sw_vers is always present.
// env is the explicit subprocess environment (DEV §12).
func hostMacOSMajor(env []string) (int, error) {
	out, err := sysexec.Command(env, "sw_vers", "-productVersion").Output()
	if err != nil {
		return 0, fmt.Errorf("sw_vers: %w", err)
	}
	major, _, _ := strings.Cut(strings.TrimSpace(string(out)), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0, fmt.Errorf("parse macOS version %q: %w", string(out), err)
	}
	return n, nil
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
	cmd := sysexec.CommandContext(ctx, r.execEnv, r.tartBin, args...)
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
	cmd := sysexec.CommandContext(ctx, r.execEnv, r.tartBin, args...)
	cmd.Stdin = strings.NewReader(r.buildInfoContent(baseImage))
	return cmd.Run()
}

// pullImage pulls a Tart VM image from a registry.
func (r *Runtime) pullImage(ctx context.Context, imageRef string, output io.Writer) error {
	cmd := sysexec.CommandContext(ctx, r.execEnv, r.tartBin, "pull", imageRef)
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

	// Start the VM in the background for provisioning.
	// Use sysexec.Command (not CommandContext) because the provisioning VM must
	// run past the per-step context; the outer function's defer stops it on return.
	cmd := sysexec.Command(r.execEnv, r.tartBin, "run", "--no-graphics", vmName)
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
		stopCmd := sysexec.CommandContext(ctx, r.execEnv, r.tartBin, "stop", vmName)
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
		provCmd := sysexec.CommandContext(ctx, r.execEnv, r.tartBin, args...)
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
	shutCmd := sysexec.CommandContext(ctx, r.execEnv, r.tartBin, shutArgs...)
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

// BaseExists checks if a base VM exists.
func (r *Runtime) BaseExists(ctx context.Context, baseName string) (bool, error) {
	return r.vmExists(ctx, baseName), nil
}

// CreateBase creates a new runtime base image with specified runtimes.
// Progress is written to progress (the caller's writer); the library never
// touches the process's os.Stdout/Stderr (§12).
func (r *Runtime) CreateBase(ctx context.Context, baseName string, runtimes []RuntimeVersion, progress io.Writer) error {
	tempVM := generateTempVMName(baseName)
	defer r.cleanupTempVM(ctx, tempVM) // Always cleanup temp VM

	// Clone yoloai-base to temp VM
	if _, err := r.runTart(ctx, "clone", "yoloai-base", tempVM); err != nil {
		return fmt.Errorf("clone base: %w", err)
	}

	// Start temp VM for runtime installation
	if err := r.startTempVM(ctx, tempVM); err != nil {
		return fmt.Errorf("start temp VM: %w", err)
	}
	defer r.stopVM(ctx, tempVM) // Ensure VM stopped before snapshot

	// Configure Xcode in VM (required for xcodebuild)
	fmt.Fprintf(progress, "Configuring Xcode...\n") //nolint:errcheck // best-effort progress
	if err := r.configureXcodeInVM(ctx, tempVM); err != nil {
		return fmt.Errorf("configure Xcode: %w", err)
	}

	// Copy each runtime into the VM
	for _, rt := range runtimes {
		fmt.Fprintf(progress, "Copying %s %s runtime (this may take several minutes)...\n", rt.Platform, rt.Version) //nolint:errcheck // best-effort progress
		if err := CopyRuntimeToVM(ctx, r.execEnv, r.tartBin, tempVM, rt, progress); err != nil {
			return fmt.Errorf("copy %s %s: %w", rt.Platform, rt.Version, err)
		}
	}

	// Stop VM to flush all changes to disk
	r.stopVM(ctx, tempVM)

	// Wait for VM to fully stop
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !r.isRunning(ctx, tempVM) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Snapshot temp VM as new base
	if err := r.snapshotAsBase(ctx, tempVM, baseName); err != nil {
		return fmt.Errorf("snapshot base: %w", err)
	}

	return nil
}

// generateTempVMName generates a unique temporary VM name.
func generateTempVMName(baseName string) string {
	// Generate 6 random hex chars
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	random := fmt.Sprintf("%x", b)
	return fmt.Sprintf("%s-tmp-%s", baseName, random)
}

// startTempVM starts a temporary VM for runtime installation.
func (r *Runtime) startTempVM(ctx context.Context, vmName string) error {
	// Build arguments for tart run
	args := []string{"run", "--no-graphics"}

	// Mount Xcode (required for xcodebuild to work)
	if xcodeDevPath := getXcodeSelectPath(r.execEnv); xcodeDevPath != "" {
		// xcode-select returns: /Applications/Xcode.app/Contents/Developer
		// We need: /Applications/Xcode.app
		xcodePath := filepath.Dir(filepath.Dir(xcodeDevPath))
		mountName := "m-" + filepath.Base(xcodePath)
		args = append(args, "--dir", fmt.Sprintf("%s:%s:ro", mountName, xcodePath))

		// Also mount PrivateFrameworks from the same Xcode installation
		privateFrameworks := filepath.Join(filepath.Dir(xcodeDevPath), "PrivateFrameworks")
		if info, err := os.Stat(privateFrameworks); err == nil && info.IsDir() {
			args = append(args, "--dir", "m-PrivateFrameworks:"+privateFrameworks+":ro")
		}
	}

	// Mount /Library/Developer/CoreSimulator/Volumes/ (not needed for xcodebuild, but kept for consistency)
	volumesPath := "/Library/Developer/CoreSimulator/Volumes"
	args = append(args, "--dir", "m-Volumes:"+volumesPath+":ro")

	// Mount /tmp
	args = append(args, "--dir", "m-tmp:/tmp:ro")

	// Add VM name
	args = append(args, vmName)

	// Start VM in background, capturing output to a temp log so a failed boot
	// can be diagnosed (notably Apple's concurrent-VM cap, where tart run exits
	// immediately). A file — not a bytes.Buffer — because os/exec's output-copy
	// goroutine may still be writing when waitForBoot times out; reading the
	// buffer concurrently would be a data race, but a file read is not.
	runLog, err := os.CreateTemp("", "yoloai-tart-tmp-*.log")
	if err != nil {
		return fmt.Errorf("create VM log: %w", err)
	}
	runLogPath := runLog.Name()
	defer os.Remove(runLogPath) //nolint:errcheck // best-effort cleanup

	// Use sysexec.Command (not CommandContext) because the temp VM must run past
	// the per-operation context; stopVM tears it down explicitly on return.
	cmd := sysexec.Command(r.execEnv, r.tartBin, args...)
	cmd.Stdout = runLog
	cmd.Stderr = runLog
	if err := cmd.Start(); err != nil {
		_ = runLog.Close()
		return fmt.Errorf("start VM: %w", err)
	}

	// Wait for boot
	procDone := make(chan error, 1)
	go func() {
		procDone <- cmd.Wait()
		_ = runLog.Close()
	}()

	if err := r.waitForBoot(ctx, vmName, procDone); err != nil {
		if logData, readErr := os.ReadFile(runLogPath); readErr == nil { //nolint:gosec // G304: temp file we created
			if limitErr := checkVMLimitError(string(logData)); limitErr != nil {
				return limitErr
			}
		}
		return err
	}
	return nil
}

// configureXcodeInVM sets up Xcode symlinks and configuration inside the VM.
func (r *Runtime) configureXcodeInVM(ctx context.Context, vmName string) error {
	// Get active Xcode path on host
	xcodeDevPath := getXcodeSelectPath(r.execEnv)
	if xcodeDevPath == "" {
		return fmt.Errorf("no active Xcode found (run xcode-select on host)")
	}

	// xcode-select returns: /Applications/Xcode.app/Contents/Developer
	// We need: /Applications/Xcode.app
	xcodePath := filepath.Dir(filepath.Dir(xcodeDevPath))
	xcodeName := filepath.Base(xcodePath)

	// VirtioFS mount point inside VM
	vfsMountPoint := filepath.Join(sharedDirVMPath, "m-"+xcodeName)

	// Create symlink from VirtioFS mount to expected location
	symlinkCmd := fmt.Sprintf("sudo rm -rf '%s' && sudo ln -sf '%s' '%s'", xcodePath, vfsMountPoint, xcodePath)
	args := execArgs(vmName, "bash", "-c", symlinkCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		return fmt.Errorf("create Xcode symlink: %w", err)
	}

	// Also symlink PrivateFrameworks if it's mounted
	privateFrameworks := filepath.Join(filepath.Dir(xcodeDevPath), "PrivateFrameworks")
	if info, err := os.Stat(privateFrameworks); err == nil && info.IsDir() {
		vfsPrivate := filepath.Join(sharedDirVMPath, "m-PrivateFrameworks")
		symlinkPrivateCmd := fmt.Sprintf("sudo rm -rf '%s' && sudo ln -sf '%s' '%s'", privateFrameworks, vfsPrivate, privateFrameworks)
		args = execArgs(vmName, "bash", "-c", symlinkPrivateCmd)
		if _, err := r.runTart(ctx, args...); err != nil {
			// Non-fatal: PrivateFrameworks might not be critical
			slog.Debug("failed to symlink PrivateFrameworks", "err", err)
		}
	}

	// Set active developer directory
	xcodeSelectCmd := fmt.Sprintf("sudo xcode-select -s '%s/Contents/Developer'", xcodePath)
	args = execArgs(vmName, "bash", "-c", xcodeSelectCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		return fmt.Errorf("run xcode-select: %w", err)
	}

	// Accept Xcode license (non-interactive)
	acceptLicenseCmd := "sudo xcodebuild -license accept"
	args = execArgs(vmName, "bash", "-c", acceptLicenseCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		// Non-fatal: license might already be accepted or not required
		slog.Debug("xcodebuild -license accept failed (might already be accepted)", "err", err)
	}

	// Run xcodebuild -runFirstLaunch to complete setup
	firstLaunchCmd := "sudo xcodebuild -runFirstLaunch"
	args = execArgs(vmName, "bash", "-c", firstLaunchCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		// Non-fatal: might not be needed
		slog.Debug("xcodebuild -runFirstLaunch failed (might not be needed)", "err", err)
	}

	return nil
}

// snapshotAsBase creates a new base image by cloning a temp VM.
func (r *Runtime) snapshotAsBase(ctx context.Context, tempVM, baseName string) error {
	// Clone temp VM to new base name
	if _, err := r.runTart(ctx, "clone", tempVM, baseName); err != nil {
		// If clone fails and partial base exists, delete it
		_, _ = r.runTart(ctx, "delete", baseName)
		return fmt.Errorf("clone to base: %w", err)
	}
	return nil
}

// cleanupTempVM removes a temporary VM (best-effort, never fails).
func (r *Runtime) cleanupTempVM(ctx context.Context, vmName string) {
	r.stopVM(ctx, vmName)
	_, _ = r.runTart(ctx, "delete", vmName)
}
