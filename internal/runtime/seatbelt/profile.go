package seatbelt

// ABOUTME: Generates macOS sandbox-exec SBPL profiles from runtime config.

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// GenerateProfile builds an SBPL profile string from the instance
// configuration, sandbox directory path, and user's home directory.
func GenerateProfile(cfg runtime.InstanceConfig, sandboxDir, homeDir string) string {
	var b strings.Builder

	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n\n")

	// --- Process control ---
	b.WriteString("; Process execution and signals\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow signal)\n\n")

	// --- System information ---
	b.WriteString("; System information\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow file-read-metadata)\n\n")

	// --- Root directory entry (needed by bash/dyld to resolve paths) ---
	b.WriteString("; Root directory listing\n")
	b.WriteString("(allow file-read* (literal \"/\"))\n\n")

	// --- System libraries and binaries ---
	b.WriteString("; System libraries, frameworks, and binaries\n")
	for _, path := range systemReadPaths() {
		b.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n", path))
	}
	b.WriteString("\n")

	// --- Temp directories ---
	b.WriteString("; Temporary directories\n")
	for _, path := range tempPaths() {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* (subpath %q))\n", path))
	}
	b.WriteString("\n")

	// --- IPC ---
	b.WriteString("; Mach and IPC (permissive initially)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-shm-read-data)\n")
	b.WriteString("(allow ipc-posix-shm-write-data)\n")
	b.WriteString("(allow ipc-posix-shm-write-create)\n")
	b.WriteString("(allow ipc-posix-sem)\n\n")

	// --- Sandbox directory (always read-write) ---
	b.WriteString("; Sandbox directory\n")
	b.WriteString(fmt.Sprintf("(allow file-read* file-write* (subpath %q))\n\n", sandboxDir))

	// --- Mount-derived filesystem rules ---
	b.WriteString("; Mount-derived filesystem rules\n")
	for _, m := range cfg.Mounts {
		if m.Source == "" {
			continue
		}
		if m.ReadOnly {
			b.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n", m.Source))
		} else {
			b.WriteString(fmt.Sprintf("(allow file-read* file-write* (subpath %q))\n", m.Source))
		}
	}
	b.WriteString("\n")

	// --- Home directory (read for .gitconfig etc.) ---
	b.WriteString("; Home directory (limited read access)\n")
	b.WriteString(fmt.Sprintf("(allow file-read* (subpath %q))\n\n", homeDir))

	// --- Network ---
	b.WriteString("; Network access\n")
	if cfg.NetworkMode != "none" {
		b.WriteString("(allow network*)\n")
	}
	b.WriteString("\n")

	// --- Pseudo-terminals ---
	b.WriteString("; Pseudo-terminal access (required for tmux/agent)\n")
	b.WriteString("(allow file-ioctl)\n") // terminal control (tcsetattr, TIOCGWINSZ, etc.)
	b.WriteString("(allow file-read* file-write* (regex #\"/dev/pty.*\"))\n")
	b.WriteString("(allow file-read* file-write* (regex #\"/dev/tty.*\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/ptmx\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/null\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/random\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/urandom\"))\n")

	return b.String()
}

// systemReadPaths returns paths that should be readable for system
// libraries, frameworks, and standard executables.
func systemReadPaths() []string {
	return []string{
		"/usr/lib",
		"/usr/bin",
		"/usr/sbin",
		"/usr/local",
		"/usr/share",
		"/bin",
		"/sbin",
		"/System",
		"/Library",
		"/private/etc",
		"/opt/homebrew",     // Apple Silicon Homebrew
		"/usr/local/Cellar", // Intel Homebrew
		"/usr/local/opt",    // Intel Homebrew symlinks
		"/usr/local/bin",    // Intel Homebrew binaries
		"/usr/local/lib",    // Intel Homebrew libraries
		"/Applications",
		"/var/run",
		"/var/db",
		"/dev",
	}
}

// tempPaths returns paths for temporary file storage.
func tempPaths() []string {
	return []string{
		"/tmp",
		"/private/tmp",
		"/private/var/folders",
	}
}
