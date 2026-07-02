package caps

// ABOUTME: DetectEnvironment() probes the host environment (root, WSL2, container, KVM group)
// ABOUTME: using injectable file path vars for testability.

import (
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// Injectable file path vars for testing.
var (
	procVersionPath = "/proc/version"  // IsWSL2
	dockerEnvPath   = "/.dockerenv"    // InContainer
	cgroupPath      = "/proc/1/cgroup" // InContainer (fallback)
	groupFilePath   = "/etc/group"     // KVMGroup
)

// DetectEnvironment probes the host and returns an Environment struct.
// The result is computed once and passed to all Permanent and Fix calls.
func DetectEnvironment() Environment {
	return Environment{
		IsRoot:      detectIsRoot(),
		IsWSL2:      detectIsWSL2(),
		InContainer: detectInContainer(),
		KVMGroup:    detectKVMGroup(),
	}
}

func detectIsRoot() bool {
	return fileutil.ProcessIsRoot()
}

func detectIsWSL2() bool {
	data, err := os.ReadFile(procVersionPath)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func detectInContainer() bool {
	// /.dockerenv exists in Docker containers.
	if _, err := os.Stat(dockerEnvPath); err == nil {
		return true
	}
	// Fall back to cgroup inspection.
	data, err := os.ReadFile(cgroupPath)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "docker") ||
		strings.Contains(lower, "lxc") ||
		strings.Contains(lower, "kubepods")
}

func detectKVMGroup() bool {
	data, err := os.ReadFile(groupFilePath)
	if err != nil {
		return false
	}

	// Resolve the username deterministically from the host UID. This is more
	// reliable than $USER (which sudo/su can leave naming the wrong user) and
	// keys on the same /etc/passwd identity that /etc/group lists below.
	username := usernameFromPasswd(fileutil.HostUID())
	if username == "" {
		return false
	}

	// Look for a "kvm:" line that contains the username.
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "kvm:") {
			continue
		}
		// Format: kvm:x:GID:user1,user2,...
		parts := strings.SplitN(line, ":", 4)
		if len(parts) < 4 {
			continue
		}
		members := strings.SplitSeq(parts[3], ",")
		for m := range members {
			if strings.TrimSpace(m) == username {
				return true
			}
		}
	}
	return false
}

// usernameFromPasswd looks up the username for uid in /etc/passwd.
func usernameFromPasswd(uid int) string {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	uidStr := itoa(uid)
	for line := range strings.SplitSeq(string(data), "\n") {
		parts := strings.SplitN(line, ":", 7)
		if len(parts) < 4 {
			continue
		}
		if parts[2] == uidStr {
			return parts[0]
		}
	}
	return ""
}

// itoa converts an int to a decimal string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
