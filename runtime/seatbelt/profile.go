package seatbelt

// ABOUTME: Generates macOS sandbox-exec SBPL profiles from runtime config.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
)

// lookPath is a variable so tests can override it.
var lookPath = exec.LookPath

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
		fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", path)
	}
	b.WriteString("\n")

	// --- Detected toolchain paths ---
	toolchainPaths := toolchainReadPaths()
	if len(toolchainPaths) > 0 {
		b.WriteString("; Detected toolchain installation prefixes\n")
		for _, path := range toolchainPaths {
			for _, p := range resolvePathVariants(path) {
				fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", p)
			}
		}
		b.WriteString("\n")
	}

	// --- Temp directories ---
	b.WriteString("; Temporary directories\n")
	for _, path := range tempPaths() {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", path)
	}
	b.WriteString("\n")

	// --- IPC ---
	b.WriteString("; Mach and IPC\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-shm-read-data)\n")
	b.WriteString("(allow ipc-posix-shm-write-data)\n")
	b.WriteString("(allow ipc-posix-shm-write-create)\n")
	b.WriteString("(allow ipc-posix-sem)\n\n")

	// --- Sandbox directory (always read-write) ---
	b.WriteString("; Sandbox directory\n")
	for _, p := range resolvePathVariants(sandboxDir) {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	b.WriteString("\n")

	// --- Mount-derived filesystem rules ---
	b.WriteString("; Mount-derived filesystem rules\n")
	for _, m := range cfg.Mounts {
		if m.Source == "" {
			continue
		}
		for _, src := range resolvePathVariants(m.Source) {
			if m.ReadOnly {
				fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", src)
			} else {
				fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", src)
			}
		}
	}
	b.WriteString("\n")

	// --- Home directory (minimal access — credentials excluded by default) ---
	b.WriteString("; Home directory (agent binaries and git config only)\n")
	for _, p := range resolvePathVariants(filepath.Join(homeDir, ".local")) {
		fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, ".gitconfig")) {
		fmt.Fprintf(&b, "(allow file-read* (literal %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, ".config", "git")) {
		fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", p)
	}
	b.WriteString("\n")

	// --- iOS/Xcode development directories ---
	b.WriteString("; iOS/Xcode development (SwiftPM caches and Xcode metadata)\n")
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "Caches", "org.swift.swiftpm")) {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "Developer", "Xcode")) {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "Caches", "swift-build")) {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "org.swift.swiftpm")) {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	b.WriteString("\n")

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

// toolchainReadPaths discovers installation prefixes for common toolchain
// binaries (python3, node, ruby, go, rustc, java) by resolving their paths
// at runtime. Prefixes that are already covered by systemReadPaths(), equal
// to "/", or have fewer than 3 path components are skipped.
func toolchainReadPaths() []string {
	toolchains := []string{"python3", "node", "ruby", "go", "rustc", "java"}
	sysPaths := systemReadPaths()
	seen := make(map[string]bool)
	var result []string

	for _, name := range toolchains {
		binPath, err := lookPath(name)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(binPath)
		if err != nil {
			continue
		}
		prefix := filepath.Dir(filepath.Dir(resolved))

		// Skip root prefix.
		if prefix == "/" {
			continue
		}

		// Skip prefixes with fewer than 3 path components.
		parts := strings.Split(prefix, "/")
		count := 0
		for _, p := range parts {
			if p != "" {
				count++
			}
		}
		if count < 2 {
			continue
		}

		// Skip if already covered by a system read path.
		covered := false
		for _, sysPath := range sysPaths {
			if prefix == sysPath || strings.HasPrefix(prefix, sysPath+"/") {
				covered = true
				break
			}
		}
		if covered {
			continue
		}

		// Deduplicate.
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		result = append(result, prefix)
	}

	return result
}

// resolvePathVariants returns the path variants needed for SBPL rules.
// macOS sandbox-exec checks file access at the vnode level (after symlink
// resolution), so if any component of a path is a symlink the SBPL subpath
// rule must include the resolved path. When the resolved path differs from
// the original, both are returned so the rule covers either access pattern.
func resolvePathVariants(path string) []string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved == path {
		return []string{path}
	}
	return []string{resolved, path}
}
