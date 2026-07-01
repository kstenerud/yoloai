//go:build windows

// ABOUTME: Windows stub of fileOwnerUID — overlay sandboxes are Linux-only, so
// ABOUTME: the overlay-flatten migrator's host-ownership preflight never runs here.

package orchestrator

import "io/fs"

// fileOwnerUID cannot determine ownership on Windows; overlay sandboxes require
// a Linux kernel, so this stub is never hit in practice.
func fileOwnerUID(fs.FileInfo) (int, bool) { return 0, false }
