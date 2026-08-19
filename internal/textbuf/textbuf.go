// ABOUTME: Printf for an in-memory text buffer. It is the printf shape without
// ABOUTME: fmt.Fprintf's io.Writer, which is what lets library code keep the
// ABOUTME: idiom for building strings while the threaded writer stays banned:
// ABOUTME: this one cannot reach a caller's stream at all.

// Package textbuf provides printf-style appending to an in-memory text buffer.
//
// It exists because of a conflict between two things yoloAI wants at once.
// `fmt.Fprint*` is banned in library code (D145) — it is how a threaded
// io.Writer comes back, and the ban has to be absolute because `forbidigo`
// matches call names rather than argument types, so it cannot tell a write to a
// caller's stream from a write to a local builder. But that ban also outlaws
// `fmt.Fprintf(&b, …)`, which is the normal way to build a string in Go, and
// the replacement — `b.WriteString(fmt.Sprintf(…))` — is verbose enough that
// staticcheck has a check (QF1012) telling you to undo it.
//
// Printf resolves that by making the distinction structural instead of lexical.
// It accepts a Buffer, not an io.Writer, so no amount of misuse turns it into
// the mechanism the ban exists to prevent — the type system enforces what a
// name-matching linter could only approximate.
package textbuf

import "fmt"

// Buffer is an in-memory text accumulator: *strings.Builder or *bytes.Buffer.
//
// String() is in the interface to *exclude* things, not because Printf calls
// it. WriteString alone would also admit *os.File and *bufio.Writer — real
// destinations, which is exactly what must not be reachable from here.
// Requiring String() as well narrows the set to types that accumulate text in
// memory and can hand it back, which no caller-supplied stream does.
type Buffer interface {
	WriteString(string) (int, error)
	String() string
}

// Printf appends the formatted string to b.
//
// The write error is dropped because neither implementation can produce one:
// *strings.Builder and *bytes.Buffer return nil unless the process is already
// out of memory, at which point the append is not the problem.
func Printf(b Buffer, format string, args ...any) {
	_, _ = b.WriteString(fmt.Sprintf(format, args...))
}
