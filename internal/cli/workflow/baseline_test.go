package workflow

// ABOUTME: Unit tests for baseline command helpers.

import (
	"bytes"
	"testing"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBaselineCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	return cmd, &buf
}

// TestPrintBaselineChange_SubjectPresent verifies that when the baseline change
// carries a subject, the output contains "Baseline advanced to", the truncated
// SHA, and the subject in parentheses.
func TestPrintBaselineChange_SubjectPresent(t *testing.T) {
	cmd, buf := newBaselineCmd(t)
	change := &yoloai.BaselineChange{NewSHA: "abcdef1234567890", Subject: "fix: something"}

	require.NoError(t, printBaselineChange(cmd, "mybox", "", change))

	out := buf.String()
	assert.Contains(t, out, "Baseline advanced to")
	assert.Contains(t, out, "abcdef12") // short8 of NewSHA
	assert.Contains(t, out, "fix: something")
}

// TestPrintBaselineChange_NoSubject verifies that when Subject is empty, the
// baseline line is still emitted (without parenthesised subject).
func TestPrintBaselineChange_NoSubject(t *testing.T) {
	cmd, buf := newBaselineCmd(t)
	change := &yoloai.BaselineChange{NewSHA: "abcdef1234567890", Subject: ""}

	require.NoError(t, printBaselineChange(cmd, "mybox", "", change))

	out := buf.String()
	assert.Contains(t, out, "Baseline advanced to")
	assert.Contains(t, out, "abcdef12") // short8 of NewSHA
	assert.NotContains(t, out, "(")     // no parenthesised subject
}

// TestPrintBaselineChange_WithOldSHA verifies that when oldSHA is non-empty an
// undo-hint line is printed, containing the short old SHA and the sandbox name.
func TestPrintBaselineChange_WithOldSHA(t *testing.T) {
	cmd, buf := newBaselineCmd(t)
	change := &yoloai.BaselineChange{NewSHA: "abcdef1234567890", Subject: "fix: something"}

	require.NoError(t, printBaselineChange(cmd, "mybox", "oldsha1234567890", change))

	out := buf.String()
	assert.Contains(t, out, "oldsha12") // short8 of oldSHA
	assert.Contains(t, out, "mybox")
	assert.Contains(t, out, "undo")
}

// TestPrintBaselineChange_NoOldSHA verifies that when oldSHA is empty no
// undo-hint line is emitted.
func TestPrintBaselineChange_NoOldSHA(t *testing.T) {
	cmd, buf := newBaselineCmd(t)
	change := &yoloai.BaselineChange{NewSHA: "abcdef1234567890", Subject: "fix: something"}

	require.NoError(t, printBaselineChange(cmd, "mybox", "", change))

	out := buf.String()
	assert.NotContains(t, out, "Previous baseline")
	assert.NotContains(t, out, "undo")
}
