package seatbelt

// ABOUTME: Prerequisite verification for the seatbelt backend. No image to build.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"

	"github.com/kstenerud/yoloai/config"
)

// requiredBinaries lists the executables that must be present for seatbelt.
var requiredBinaries = []string{
	"sandbox-exec",
	"tmux",
	"jq",
}

// Setup verifies that all prerequisites are available. There is no image to
// build — seatbelt runs the host's native tools. The sourceDir and layout
// parameters are unused; they are accepted to satisfy the runtime.Runtime
// interface (Q-W.5).
func (r *Runtime) Setup(_ context.Context, _ config.Layout, _ string, output io.Writer, _ *slog.Logger, _ bool) error {
	for _, bin := range requiredBinaries {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH: install it before using the seatbelt backend", bin)
		}
	}
	fmt.Fprintln(output, "Seatbelt prerequisites verified (sandbox-exec, tmux, jq).") //nolint:errcheck // best-effort
	return nil
}

// IsReady returns true when all prerequisite binaries are available.
func (r *Runtime) IsReady(_ context.Context) (bool, error) {
	for _, bin := range requiredBinaries {
		if _, err := exec.LookPath(bin); err != nil {
			return false, nil //nolint:nilerr // binary not found means unavailable, not an error condition
		}
	}
	return true, nil
}
