//go:build linux

package yoloai

// ABOUTME: Platform-specific runtime imports for Linux (includes containerd).

import (
	_ "github.com/kstenerud/yoloai/runtime/containerd" // register backend
	_ "github.com/kstenerud/yoloai/runtime/microvm"    // register backend
)
