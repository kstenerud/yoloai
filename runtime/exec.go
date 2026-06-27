package runtime

// ABOUTME: Shared helper for running exec.Cmd and building ExecResult.

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ExecError is returned by RunCmdExec/RunCmdExecRaw when the command runs but
// exits with a non-zero code. Carrying the code as a field (not embedded in
// the error string) lets callers match exit codes via errors.As instead of
// string-matching the error message. W8 of the architecture remediation plan.
type ExecError struct {
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("exec exited with code %d: %s", e.ExitCode, e.Stderr)
}

// InteractiveExitError normalizes the error from an interactive exec into the
// runtime's uniform contract: a clean non-zero exit becomes an *ExecError
// carrying the code (Stderr empty — the streams were wired to the caller, not
// captured), any other failure passes through, and nil stays nil. Backends that
// shell out hand it exec.Cmd.Run's result; the result is that every backend's
// InteractiveExec surfaces a non-zero inner exit the same way, so the CLI can
// extract the code with one errors.As regardless of backend.
func InteractiveExitError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &ExecError{ExitCode: exitErr.ExitCode()}
	}
	return err
}

// RunCmdExec runs an exec.Cmd, captures stdout/stderr, and returns an
// ExecResult. On non-zero exit the result is returned alongside an *ExecError
// containing the exit code and stderr. Non-ExitError failures (e.g.
// binary not found) are wrapped and returned directly.
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
		return result, &ExecError{ExitCode: exitCode, Stderr: strings.TrimSpace(stderr.String())}
	}

	return result, nil
}

// RunCmdExecRaw is like RunCmdExec but preserves exact stdout/stderr without
// trimming whitespace. Use this for commands (like git diff) whose output is
// whitespace-sensitive.
func RunCmdExecRaw(cmd *exec.Cmd) (ExecResult, error) {
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
		Stdout:   stdout.String(),
		ExitCode: exitCode,
	}

	if exitCode != 0 {
		return result, &ExecError{ExitCode: exitCode, Stderr: stderr.String()}
	}

	return result, nil
}
