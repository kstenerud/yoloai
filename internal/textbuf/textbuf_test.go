// ABOUTME: Printf's contract, and the type-level guarantee that is the reason
// ABOUTME: it exists: a Buffer is an in-memory accumulator, so an io.Writer —
// ABOUTME: the mechanism D145 removed — cannot be passed to it at all.

package textbuf_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/textbuf"
)

// TestPrintf_AppendsToEitherBuffer covers both implementations, since the
// interface exists to admit exactly these two.
func TestPrintf_AppendsToEitherBuffer(t *testing.T) {
	var sb strings.Builder
	textbuf.Printf(&sb, "%s=%d", "port", 8080)
	textbuf.Printf(&sb, ";%s", "more")
	if sb.String() != "port=8080;more" {
		t.Errorf("strings.Builder = %q", sb.String())
	}

	var bb bytes.Buffer
	textbuf.Printf(&bb, "%s=%d", "port", 8080)
	if bb.String() != "port=8080" {
		t.Errorf("bytes.Buffer = %q", bb.String())
	}
}

// TestArch_BufferExcludesRealDestinations is the claim the package exists for:
// Printf cannot be aimed at a caller's stream.
//
// It is a compile-time property, so the test asserts it the only way a test
// can — by checking the type assertions that would have to succeed for the
// misuse to be possible, and requiring that they do not. *os.File is the case
// that matters: it has WriteString, so an interface asking only for that would
// have admitted it, and Printf would then be fmt.Fprintf wearing a different
// name.
func TestArch_BufferExcludesRealDestinations(t *testing.T) {
	var asBuffer any = os.Stdout
	if _, ok := asBuffer.(textbuf.Buffer); ok {
		t.Error("*os.File satisfies textbuf.Buffer — Printf can be aimed at a real stream, " +
			"which makes it the threaded writer D145 removed, under a new name")
	}

	// The positive half, so the exclusion above cannot pass by the interface
	// becoming unsatisfiable.
	var sb any = &strings.Builder{}
	if _, ok := sb.(textbuf.Buffer); !ok {
		t.Error("*strings.Builder does not satisfy textbuf.Buffer — the interface excludes everything")
	}
}
