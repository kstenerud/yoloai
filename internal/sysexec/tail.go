// ABOUTME: TailBuffer — an io.Writer that keeps only the last N lines written,
// ABOUTME: so a failed subprocess's error can carry the tail of its output
// ABOUTME: without buffering a whole (potentially huge) build log.

package sysexec

import (
	"strings"
	"sync"
)

// TailBuffer is an io.Writer that retains only the last maxLines lines written
// to it. It exists because the image-build path streams a subprocess's combined
// stdout+stderr straight to the user's terminal but, on failure, returns only a
// generic "build exited with code N": the actionable cause (a permission error
// on ~/.docker, a missing apt package, a network failure) is visible
// interactively yet absent from the error object, so `--json` output and library
// embedders — who never see the stream — get nothing to act on (DF144).
//
// Wiring: build the command's writer as io.MultiWriter(streamTarget, tail) and
// assign the SAME value to both cmd.Stdout and cmd.Stderr, so os/exec keeps its
// single-pipe path (interfaceEqual) and the stream target still sees exactly
// what it did before. TailBuffer is mutex-guarded regardless, so a future
// two-pipe path cannot race it.
type TailBuffer struct {
	maxLines int
	mu       sync.Mutex
	lines    []string
	partial  []byte
}

// NewTailBuffer returns a TailBuffer retaining the last maxLines lines (at least 1).
func NewTailBuffer(maxLines int) *TailBuffer {
	if maxLines < 1 {
		maxLines = 1
	}
	return &TailBuffer{maxLines: maxLines}
}

// Write records p, splitting on newlines and keeping only the tail. Carriage
// returns are dropped so BuildKit's progress lines read cleanly. Never errors.
func (b *TailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range p {
		switch c {
		case '\n':
			b.lines = append(b.lines, string(b.partial))
			b.partial = b.partial[:0]
			b.compact()
		case '\r':
			// drop — progress redraws would otherwise clutter the tail
		default:
			b.partial = append(b.partial, c)
		}
	}
	return len(p), nil
}

// compact bounds retained memory to ~2*maxLines lines by copying the tail into a
// fresh slice (a plain reslice would pin the whole log's backing array).
func (b *TailBuffer) compact() {
	if len(b.lines) >= 2*b.maxLines {
		b.lines = append([]string(nil), b.lines[len(b.lines)-b.maxLines:]...)
	}
}

// String returns the last maxLines lines (including any unterminated trailing
// line) joined by "\n", surrounding blank lines trimmed. Empty if nothing
// meaningful was written.
func (b *TailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := b.lines
	if len(b.partial) > 0 {
		lines = append(append([]string(nil), b.lines...), string(b.partial))
	}
	if len(lines) > b.maxLines {
		lines = lines[len(lines)-b.maxLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ErrorSuffix formats the retained tail as an indented block to append to an
// error message, or "" when nothing was captured. Keeping the formatting here
// means each build call site appends `tail.ErrorSuffix()` without duplicating
// the layout or importing strings.
func (b *TailBuffer) ErrorSuffix() string {
	tail := b.String()
	if tail == "" {
		return ""
	}
	return "\nlast output:\n  " + strings.ReplaceAll(tail, "\n", "\n  ")
}
