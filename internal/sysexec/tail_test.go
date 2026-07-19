// ABOUTME: Tests for TailBuffer — line retention, memory bounding, and the
// ABOUTME: end-to-end wiring that carries a failed subprocess's stderr tail.

package sysexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// feed writes s to b, ignoring the (always-nil) error — TailBuffer.Write never
// fails, but errcheck can't know that.
func feed(b *TailBuffer, s string) {
	_, _ = b.Write([]byte(s))
}

func TestTailBuffer_KeepsOnlyLastLines(t *testing.T) {
	b := NewTailBuffer(3)
	for i := 1; i <= 10; i++ {
		feed(b, fmt.Sprintf("line %d\n", i))
	}
	got := b.String()
	want := "line 8\nline 9\nline 10"
	if got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
}

func TestTailBuffer_IncludesUnterminatedFinalLine(t *testing.T) {
	b := NewTailBuffer(5)
	// The actionable error line is often the last thing written and may lack a
	// trailing newline — it must still appear in the tail.
	feed(b, "step 1 ok\nERROR: permission denied")
	if got := b.String(); got != "step 1 ok\nERROR: permission denied" {
		t.Fatalf("tail = %q, want the unterminated final line included", got)
	}
}

func TestTailBuffer_DropsCarriageReturns(t *testing.T) {
	b := NewTailBuffer(2)
	feed(b, "downloading\rdownloading done\n")
	if got := b.String(); got != "downloadingdownloading done" {
		t.Fatalf("tail = %q, want CRs dropped", got)
	}
}

func TestTailBuffer_BoundsMemoryUnderManyLines(t *testing.T) {
	b := NewTailBuffer(4)
	for i := range 10_000 {
		feed(b, fmt.Sprintf("noise %d\n", i))
	}
	// Compaction keeps the slice from growing without bound; correctness is that
	// only the last maxLines survive regardless of how many were written.
	if lines := strings.Split(b.String(), "\n"); len(lines) != 4 {
		t.Fatalf("retained %d lines, want 4", len(lines))
	}
	if cap(b.lines) > 2*b.maxLines {
		t.Fatalf("backing slice cap %d exceeds 2*maxLines; compaction leaked", cap(b.lines))
	}
}

func TestTailBuffer_ErrorSuffixEmptyWhenNothingWritten(t *testing.T) {
	if s := NewTailBuffer(5).ErrorSuffix(); s != "" {
		t.Fatalf("ErrorSuffix = %q, want empty for no output", s)
	}
}

func TestTailBuffer_ErrorSuffixIndentsTail(t *testing.T) {
	b := NewTailBuffer(5)
	feed(b, "boom\ndetail")
	if got := b.ErrorSuffix(); got != "\nlast output:\n  boom\n  detail" {
		t.Fatalf("ErrorSuffix = %q", got)
	}
}

// The end-to-end wiring that matters: a failing subprocess's stderr, streamed to
// one target and tee'd into a TailBuffer, must surface in an error the caller
// builds — the exact shape the image-build path uses so a --json/embedder caller
// sees the cause, not just "exited with code 1".
func TestTailBuffer_CarriesFailedSubprocessStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	cmd := CommandContext(context.Background(), []string{}, "sh", "-c",
		"echo progress; echo 'ERROR: open ~/.docker/buildx/activity/default: permission denied' >&2; exit 1")

	var streamed strings.Builder
	tail := NewTailBuffer(20)
	w := io.MultiWriter(&streamed, tail)
	cmd.Stdout = w
	cmd.Stderr = w

	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected an ExitError, got %v", runErr)
	}

	wrapped := fmt.Errorf("docker build exited with code %d%s", exitErr.ExitCode(), tail.ErrorSuffix())
	if !strings.Contains(wrapped.Error(), "permission denied") {
		t.Fatalf("wrapped error dropped the actionable cause: %q", wrapped.Error())
	}
	// The stream target still saw everything, unchanged by the tee.
	if !strings.Contains(streamed.String(), "progress") {
		t.Fatalf("stream target lost output: %q", streamed.String())
	}
}
