package tart

// ABOUTME: VM provisioning for Tart: pulls base macOS image, installs dev tools.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// defaultBaseImage is the Cirrus Labs macOS base image pulled on first run.
	defaultBaseImage = "ghcr.io/cirruslabs/macos-sequoia-base:latest"

	// provisionedImageName is the local VM name for the provisioned base image.
	provisionedImageName = "yoloai-base"

	// provisionMarkerFile tracks whether the base image has been provisioned.
	provisionMarkerFile = ".tart-provisioned"
)

// provisionCommands are the shell commands to install dev tools in the base VM.
// Each entry is run via tart exec as the admin user. They install:
// - Xcode Command Line Tools, Homebrew, Node.js, tmux, git, jq, ripgrep
var provisionCommands = []string{
	// Accept Xcode license and install CLI tools (may already be present in base image)
	"sudo xcode-select --install 2>/dev/null || true",

	// Install Homebrew if not present
	`which brew >/dev/null 2>&1 || /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`,

	// Ensure Homebrew is on PATH for subsequent commands
	`eval "$(/opt/homebrew/bin/brew shellenv)" && brew install node tmux jq ripgrep`,

	// Install Claude Code via npm
	`eval "$(/opt/homebrew/bin/brew shellenv)" && npm install -g @anthropic-ai/claude-code`,

	// Add Homebrew to shell profile for future logins
	`grep -q 'brew shellenv' ~/.zprofile 2>/dev/null || echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zprofile`,
}

// EnsureImage ensures the provisioned base VM image exists, pulling and
// provisioning as needed. If imageRef is set in config (tart_image override),
// it uses that as the base instead of the default.
func (r *Runtime) EnsureImage(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	// Check if provisioned image already exists
	if !force {
		exists, err := r.ImageExists(ctx, provisionedImageName)
		if err != nil {
			return fmt.Errorf("check base image: %w", err)
		}
		if exists && r.isProvisioned(sourceDir) {
			return nil
		}
	}

	baseImage := r.resolveBaseImage(sourceDir)

	// Check if the base image needs to be pulled
	baseExists, err := r.ImageExists(ctx, baseImage)
	if err != nil {
		return fmt.Errorf("check base image: %w", err)
	}

	if !baseExists {
		fmt.Fprintf(output, "Pulling base macOS VM image (%s)...\n", baseImage) //nolint:errcheck // best-effort
		fmt.Fprintln(output, "This is a one-time download (~30 GB) and may take a while.")              //nolint:errcheck // best-effort

		if err := r.pullImage(ctx, baseImage, output); err != nil {
			return fmt.Errorf("pull base image: %w", err)
		}
	}

	// Delete existing provisioned image if rebuilding
	provExists, _ := r.ImageExists(ctx, provisionedImageName)
	if provExists {
		fmt.Fprintln(output, "Removing old provisioned image...") //nolint:errcheck // best-effort
		if _, err := r.runTart(ctx, "delete", provisionedImageName); err != nil {
			logger.Warn("failed to delete old provisioned image", "error", err)
		}
	}

	// Clone base image to create our provisioned image
	fmt.Fprintln(output, "Cloning base image for provisioning...") //nolint:errcheck // best-effort
	if _, err := r.runTart(ctx, "clone", baseImage, provisionedImageName); err != nil {
		return fmt.Errorf("clone base image: %w", err)
	}

	// Boot the provisioned image for provisioning
	fmt.Fprintln(output, "Booting VM for provisioning (installing dev tools)...") //nolint:errcheck // best-effort
	if err := r.bootForProvisioning(ctx, provisionedImageName, output, logger); err != nil {
		// Clean up on failure
		_, _ = r.runTart(ctx, "delete", provisionedImageName)
		return fmt.Errorf("provision VM: %w", err)
	}

	// Mark as provisioned
	markerPath := filepath.Join(sourceDir, provisionMarkerFile)
	_ = os.WriteFile(markerPath, []byte("1"), 0600) // best-effort

	fmt.Fprintln(output, "Base VM image provisioned successfully.") //nolint:errcheck // best-effort
	return nil
}

// ImageExists checks if a Tart VM with the given name exists locally.
func (r *Runtime) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	out, err := r.runTart(ctx, "list", "--quiet")
	if err != nil {
		return false, fmt.Errorf("list VMs: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == imageRef {
			return true, nil
		}
	}
	return false, nil
}

// resolveBaseImage returns the base image to use, checking for a config
// override in defaults.tart_image.
func (r *Runtime) resolveBaseImage(_ string) string {
	if r.baseImageOverride != "" {
		return r.baseImageOverride
	}
	return defaultBaseImage
}

// isProvisioned checks if the base image was already provisioned.
func (r *Runtime) isProvisioned(sourceDir string) bool {
	markerPath := filepath.Join(sourceDir, provisionMarkerFile)
	_, err := os.Stat(markerPath)
	return err == nil
}

// pullImage pulls a Tart VM image from a registry.
func (r *Runtime) pullImage(ctx context.Context, imageRef string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, r.tartBin, "pull", imageRef) //nolint:gosec // G204: imageRef from config
	cmd.Stdout = output
	cmd.Stderr = output
	return cmd.Run()
}

// bootForProvisioning boots a VM, runs provision commands, then shuts it down.
func (r *Runtime) bootForProvisioning(ctx context.Context, vmName string, output io.Writer, logger *slog.Logger) error {
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

	// Ensure cleanup: stop the VM when done (regardless of success/failure)
	defer func() {
		logger.Debug("stopping provisioning VM", "vm", vmName)
		stopCmd := exec.CommandContext(ctx, r.tartBin, "stop", vmName) //nolint:gosec // G204
		if err := stopCmd.Run(); err != nil {
			// Fall back to killing the process
			_ = cmd.Process.Kill()
		}
	}()

	// Wait for VM to be accessible
	fmt.Fprintln(output, "Waiting for VM to boot (macOS VMs can take 30-60s)...") //nolint:errcheck // best-effort
	if err := r.waitForBoot(ctx, vmName, procDone); err != nil {
		// Show tart run output on failure to aid debugging
		if logData, readErr := os.ReadFile(vmLogPath); readErr == nil && len(logData) > 0 { //nolint:gosec // G304: temp file we created
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

	return nil
}
