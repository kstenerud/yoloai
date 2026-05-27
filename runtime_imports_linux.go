//go:build linux

package yoloai

// ABOUTME: Platform-specific runtime imports for Linux (includes containerd).

import (
	_ "github.com/kstenerud/yoloai/internal/runtime/containerd" // register backend
)
