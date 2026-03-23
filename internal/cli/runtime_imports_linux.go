//go:build linux

package cli

// ABOUTME: Platform-specific runtime imports for Linux (includes containerd).

import (
	_ "github.com/kstenerud/yoloai/runtime/containerd" // register backend
)
