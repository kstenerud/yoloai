//go:build !windows && !linux

// ABOUTME: Non-linux unix Stat_t.Dev accessor — Dev is int32 on darwin, so the
// ABOUTME: widen to uint64 is a real (safe) conversion gosec flags as G115.
package migrate

import "syscall"

// statDev returns the device id as uint64. On darwin (and other non-linux
// unixes) Stat_t.Dev is a signed narrower type, so this is a genuine widen.
// Device ids are non-negative, so the sign-extension G115 warns about cannot
// happen; equality is all we need.
func statDev(st *syscall.Stat_t) uint64 { return uint64(st.Dev) } //nolint:gosec // G115: Dev is non-negative; widen is safe
