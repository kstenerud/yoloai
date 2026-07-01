//go:build windows

// ABOUTME: Windows stub for the single-filesystem preflight; migration is not a
// ABOUTME: Windows concern (yoloai runs under WSL = linux), so this is a no-op.
package migrate

// SameFilesystem is a no-op on Windows, which is not a migration target
// (yoloai's Windows story is WSL, i.e. linux). Provided only so the package
// compiles under a windows cross-check.
func SameFilesystem(_ ...string) error { return nil }
