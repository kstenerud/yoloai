//go:build linux

// ABOUTME: Linux Stat_t.Dev accessor — Dev is already uint64, so no conversion
// ABOUTME: (and thus no gosec/unconvert suppression) is needed here.
package migrate

import "syscall"

// statDev returns the device id as uint64. On linux Stat_t.Dev is uint64, so
// this is a plain field read.
func statDev(st *syscall.Stat_t) uint64 { return st.Dev }
