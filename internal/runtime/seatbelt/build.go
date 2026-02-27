package seatbelt

// ABOUTME: Prerequisite verification for the seatbelt backend. No image to build.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
)

// requiredBinaries lists the executables that must be present for seatbelt.
var requiredBinaries = []string{
	"sandbox-exec",
	"tmux",
	"jq",
}

// EnsureImage verifies that all prerequisites are available. There is no
// image to build — seatbelt runs the host's native tools. The sourceDir
// parameter is unused.
func (r *Runtime) EnsureImage(_ context.Context, _ string, output io.Writer, _ *slog.Logger, _ bool) error {
	for _, bin := range requiredBinaries {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found in PATH: install it before using the seatbelt backend", bin)
		}
	}
	fmt.Fprintln(output, "Seatbelt prerequisites verified (sandbox-exec, tmux, jq).") //nolint:errcheck // best-effort
	return nil
}

// ImageExists returns true when all prerequisite binaries are available.
// The imageRef parameter is unused for seatbelt.
func (r *Runtime) ImageExists(_ context.Context, _ string) (bool, error) {
	for _, bin := range requiredBinaries {
		if _, err := exec.LookPath(bin); err != nil {
			return false, nil
		}
	}
	return true, nil
}
