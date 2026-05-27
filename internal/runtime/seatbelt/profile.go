package seatbelt

// ABOUTME: Generates macOS sandbox-exec SBPL profiles from runtime config.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// lookPath is a variable so tests can override it.
var lookPath = exec.LookPath

// GenerateProfile builds an SBPL profile string from the instance
// configuration, sandbox directory path, and user's home directory.
func GenerateProfile(cfg runtime.InstanceConfig, sandboxDir, homeDir string) string {
	var b strings.Builder

	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n\n")

	writeProfileHeader(&b)
	writeProfileSystemPaths(&b)
	writeProfileSandboxDir(&b, sandboxDir)
	writeProfileMountRules(&b, cfg.Mounts)
	writeProfileHomeDir(&b, homeDir)
	writeProfileNetwork(&b, cfg.NetworkMode)
	writeProfileDevices(&b)

	return b.String()
}

// writeProfileHeader writes process, system-info, and IPC rules.
func writeProfileHeader(b *strings.Builder) {
	b.WriteString("; Process execution and signals\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow signal)\n\n")

	b.WriteString("; System information\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow file-read-metadata)\n\n")

	b.WriteString("; Root directory listing\n")
	b.WriteString("(allow file-read* (literal \"/\"))\n\n")

	b.WriteString("; Mach and IPC\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow ipc-posix-shm-read-data)\n")
	b.WriteString("(allow ipc-posix-shm-write-data)\n")
	b.WriteString("(allow ipc-posix-shm-write-create)\n")
	b.WriteString("(allow ipc-posix-sem)\n\n")
}

// writeProfileSystemPaths writes rules for system libraries, toolchains, and temp dirs.
func writeProfileSystemPaths(b *strings.Builder) {
	b.WriteString("; System libraries, frameworks, and binaries\n")
	for _, path := range systemReadPaths() {
		fmt.Fprintf(b, "(allow file-read* (subpath %q))\n", path)
	}
	b.WriteString("\n")

	toolchainPaths := toolchainReadPaths()
	if len(toolchainPaths) > 0 {
		b.WriteString("; Detected toolchain installation prefixes\n")
		for _, path := range toolchainPaths {
			for _, p := range resolvePathVariants(path) {
				fmt.Fprintf(b, "(allow file-read* (subpath %q))\n", p)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("; Temporary directories\n")
	for _, path := range tempPaths() {
		fmt.Fprintf(b, "(allow file-read* file-write* (subpath %q))\n", path)
	}
	b.WriteString("\n")
}

// writeProfileSandboxDir writes read-write rules for the sandbox directory.
func writeProfileSandboxDir(b *strings.Builder, sandboxDir string) {
	b.WriteString("; Sandbox directory\n")
	for _, p := range resolvePathVariants(sandboxDir) {
		fmt.Fprintf(b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	b.WriteString("\n")
}

// writeProfileMountRules writes filesystem rules derived from mount specs.
func writeProfileMountRules(b *strings.Builder, mounts []runtime.MountSpec) {
	b.WriteString("; Mount-derived filesystem rules\n")
	for _, m := range mounts {
		if m.HostPath == "" {
			continue
		}
		for _, src := range resolvePathVariants(m.HostPath) {
			if m.ReadOnly {
				fmt.Fprintf(b, "(allow file-read* (subpath %q))\n", src)
			} else {
				fmt.Fprintf(b, "(allow file-read* file-write* (subpath %q))\n", src)
			}
		}
	}
	b.WriteString("\n")
}

// writeProfileHomeDir writes rules for the user's home directory (minimal access).
func writeProfileHomeDir(b *strings.Builder, homeDir string) {
	b.WriteString("; Home directory (agent binaries and git config only)\n")
	for _, p := range resolvePathVariants(filepath.Join(homeDir, ".local")) {
		fmt.Fprintf(b, "(allow file-read* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, ".gitconfig")) {
		fmt.Fprintf(b, "(allow file-read* (literal %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, ".config", "git")) {
		fmt.Fprintf(b, "(allow file-read* (subpath %q))\n", p)
	}
	b.WriteString("\n")

	b.WriteString("; iOS/Xcode development (SwiftPM caches and Xcode metadata)\n")
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "Caches", "org.swift.swiftpm")) {
		fmt.Fprintf(b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "Developer", "Xcode")) {
		fmt.Fprintf(b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "Caches", "swift-build")) {
		fmt.Fprintf(b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	for _, p := range resolvePathVariants(filepath.Join(homeDir, "Library", "org.swift.swiftpm")) {
		fmt.Fprintf(b, "(allow file-read* file-write* (subpath %q))\n", p)
	}
	b.WriteString("\n")
}

// writeProfileNetwork writes network access rules.
func writeProfileNetwork(b *strings.Builder, networkMode string) {
	b.WriteString("; Network access\n")
	if networkMode != "none" {
		b.WriteString("(allow network*)\n")
	}
	b.WriteString("\n")
}

// writeProfileDevices writes pseudo-terminal and device access rules.
func writeProfileDevices(b *strings.Builder) {
	b.WriteString("; Pseudo-terminal access (required for tmux/agent)\n")
	b.WriteString("(allow file-ioctl)\n") // terminal control (tcsetattr, TIOCGWINSZ, etc.)
	b.WriteString("(allow file-read* file-write* (regex #\"/dev/pty.*\"))\n")
	b.WriteString("(allow file-read* file-write* (regex #\"/dev/tty.*\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/ptmx\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/null\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/random\"))\n")
	b.WriteString("(allow file-read* file-write* (literal \"/dev/urandom\"))\n")
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
		prefix, ok := resolveToolchainPrefix(name)
		if !ok || prefix == "/" || pathComponentCount(prefix) < 2 || isCoveredBySysPaths(prefix, sysPaths) || seen[prefix] {
			continue
		}
		seen[prefix] = true
		result = append(result, prefix)
	}

	return result
}

// resolveToolchainPrefix resolves a binary name to its installation prefix
// (two directories above the resolved binary path). Returns ("", false) on error.
func resolveToolchainPrefix(name string) (string, bool) {
	binPath, err := lookPath(name)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		return "", false
	}
	return filepath.Dir(filepath.Dir(resolved)), true
}

// pathComponentCount returns the number of non-empty path components in p.
func pathComponentCount(p string) int {
	count := 0
	for part := range strings.SplitSeq(p, "/") {
		if part != "" {
			count++
		}
	}
	return count
}

// isCoveredBySysPaths reports whether prefix is equal to or nested under any
// of the given system paths.
func isCoveredBySysPaths(prefix string, sysPaths []string) bool {
	for _, sysPath := range sysPaths {
		if prefix == sysPath || strings.HasPrefix(prefix, sysPath+"/") {
			return true
		}
	}
	return false
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
