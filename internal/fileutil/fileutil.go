// ABOUTME: os.MkdirAll/WriteFile/OpenFile wrappers that fix ownership under sudo
// ABOUTME: so ~/.yoloai files are owned by the real user, not root.
// Package fileutil provides os.MkdirAll / os.WriteFile / os.OpenFile wrappers
// that fix file ownership when yoloai is invoked via sudo.
//
// When a user runs "sudo yoloai ...", the process uid is 0 (root), so every
// file and directory created under ~/.yoloai/ ends up owned by root. sudo
// exports SUDO_UID and SUDO_GID so we can recover the invoking user's identity
// and call os.Lchown after each create operation.
package fileutil

import (
	"encoding/json"
	"io/fs"
	"os"
	"strconv"
)

// SudoUID returns the invoking user's UID when running under sudo (uid 0 +
// SUDO_UID set). Returns -1 if the process is not running under sudo.
func SudoUID() int {
	if os.Getuid() == 0 {
		if s := os.Getenv("SUDO_UID"); s != "" {
			if uid, err := strconv.Atoi(s); err == nil {
				return uid
			}
		}
	}
	return -1
}

// SudoGID returns the invoking user's primary GID when running under sudo.
// Returns -1 if the process is not running under sudo.
func SudoGID() int {
	if os.Getgid() == 0 {
		if s := os.Getenv("SUDO_GID"); s != "" {
			if gid, err := strconv.Atoi(s); err == nil {
				return gid
			}
		}
	}
	return -1
}

// HostUID returns the invoking user's UID, accounting for sudo. Under
// sudo (uid 0 + SUDO_UID set) it returns the SUDO_UID; otherwise it
// returns the process uid. This is the F31 chokepoint for "what UID
// owns the work the user is doing" — every library caller that
// previously called os.Getuid() directly now reads it from
// config.Layout.HostUID, which is populated here.
func HostUID() int {
	if uid := SudoUID(); uid != -1 {
		return uid
	}
	return os.Getuid()
}

// HostGID returns the invoking user's primary GID, accounting for sudo.
// See HostUID for the rationale.
func HostGID() int {
	if gid := SudoGID(); gid != -1 {
		return gid
	}
	return os.Getgid()
}

// ProcessIsRoot reports whether the running process has effective UID 0.
// Distinct from "is the real invoking user root?" — under sudo this
// returns true even though the real user (recovered via SUDO_UID) is
// non-root. Used by code that needs to know whether the process can
// perform privileged operations (CNI plugin exec, raw socket creation,
// etc.) rather than whose work is being represented.
func ProcessIsRoot() bool {
	return os.Getuid() == 0
}

// ChownIfSudo calls os.Lchown to transfer ownership of path to the real user
// when running under sudo. Uses Lchown so symlinks themselves are chowned
// without following them. No-op when not running under sudo.
func ChownIfSudo(path string) error {
	uid := SudoUID()
	if uid == -1 {
		return nil
	}
	return os.Lchown(path, uid, SudoGID())
}

// MkdirAll wraps os.MkdirAll and fixes ownership when running under sudo.
func MkdirAll(path string, perm fs.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	return ChownIfSudo(path)
}

// WriteFile wraps os.WriteFile and fixes ownership when running under sudo.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil { //nolint:gosec // G306: callers choose perm deliberately
		return err
	}
	return ChownIfSudo(path)
}

// OpenFile wraps os.OpenFile and fixes ownership when running under sudo.
// ChownIfSudo is only called when os.O_CREATE is set in flag — opening an
// existing file for reading or appending does not change ownership.
// The caller is responsible for closing the file.
func OpenFile(path string, flag int, perm fs.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag, perm) //nolint:gosec // G304: callers supply trusted paths
	if err != nil {
		return nil, err
	}
	if flag&os.O_CREATE != 0 {
		if err := ChownIfSudo(path); err != nil {
			f.Close() //nolint:errcheck,gosec // G104: best-effort cleanup; original error takes priority
			return nil, err
		}
	}
	return f, nil
}

// MkdirAllPerm creates a directory (and parents) then explicitly chmods it to
// bypass the process umask. Use this when the directory will be bind-mounted
// into a container that may run under a different uid (e.g. gVisor).
func MkdirAllPerm(path string, perm fs.FileMode) error {
	if err := MkdirAll(path, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm) //nolint:gosec // G302: caller is responsible for choosing the perm
}

// WriteFilePerm writes data to a file then explicitly chmods it to bypass the
// process umask. Use this when the file will be bind-mounted into a container
// that may run under a different uid (e.g. gVisor).
func WriteFilePerm(path string, data []byte, perm fs.FileMode) error {
	if err := WriteFile(path, data, perm); err != nil { //nolint:gosec // G703: path is always a trusted sandbox subpath
		return err
	}
	return os.Chmod(path, perm) //nolint:gosec // G302: caller is responsible for choosing the perm
}

// ReadJSONMap reads a JSON file into a map, returning an empty map if the file doesn't exist.
func ReadJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// WriteJSONMap marshals a map and writes it as indented JSON to the given path.
func WriteJSONMap(path string, m map[string]any) error {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, out, 0600)
}
