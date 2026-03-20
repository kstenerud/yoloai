// Package fileutil provides os.MkdirAll / os.WriteFile / os.OpenFile wrappers
// that fix file ownership when yoloai is invoked via sudo.
//
// When a user runs "sudo yoloai ...", the process uid is 0 (root), so every
// file and directory created under ~/.yoloai/ ends up owned by root. sudo
// exports SUDO_UID and SUDO_GID so we can recover the invoking user's identity
// and call os.Lchown after each create operation.
package fileutil

import (
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
// Intended for O_CREATE calls; the caller is responsible for closing the file.
func OpenFile(path string, flag int, perm fs.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag, perm) //nolint:gosec // G304: callers supply trusted paths
	if err != nil {
		return nil, err
	}
	if err := ChownIfSudo(path); err != nil {
		f.Close() //nolint:errcheck,gosec // G104: best-effort cleanup; original error takes priority
		return nil, err
	}
	return f, nil
}
