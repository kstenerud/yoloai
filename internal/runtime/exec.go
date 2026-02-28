package runtime

// ABOUTME: Shared helper for running exec.Cmd and building ExecResult.

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// RunCmdExec runs an exec.Cmd, captures stdout/stderr, and returns an
// ExecResult. On non-zero exit the result is returned alongside an error
// containing the exit code and stderr. Non-ExitError failures (e.g.
// binary not found) are returned directly.
func RunCmdExec(cmd *exec.Cmd) (ExecResult, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok { //nolint:errorlint // ExitError is concrete type
			exitCode = exitErr.ExitCode()
		} else {
			return ExecResult{}, fmt.Errorf("exec: %w", err)
		}
	}

	result := ExecResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		ExitCode: exitCode,
	}

	if exitCode != 0 {
		return result, fmt.Errorf("exec exited with code %d: %s", exitCode, strings.TrimSpace(stderr.String()))
	}

	return result, nil
}
