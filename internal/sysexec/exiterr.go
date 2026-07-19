// ABOUTME: Surfaces the stderr an *exec.ExitError already captured, so a
// ABOUTME: `.Output()` failure explains itself instead of reading "exit status N".

package sysexec

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// EnrichExitError returns err with the stderr the command captured folded into
// its message, so a caller that ran a subprocess via Cmd.Output() surfaces the
// command's own diagnostic rather than a bare "exit status N". Output() (and
// any Run with Stderr left nil) populates (*exec.ExitError).Stderr for exactly
// this purpose, and the default "%w" rendering then throws it away.
//
// err passes through unchanged when it is nil, is not an *exec.ExitError, or
// carried no stderr — including the fail-to-start case (*exec.Error), which has
// no captured output to add. The original error is wrapped with "%w", so an
// errors.As/Is match on *exec.ExitError still succeeds through the result.
//
// This is the shared convention for DF145's ".Output()" sites; callers wrap the
// result with their own context noun, e.g.
// fmt.Errorf("podman machine inspect: %w", sysexec.EnrichExitError(err)).
func EnrichExitError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}
