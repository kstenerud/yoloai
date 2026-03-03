package sandbox

// ABOUTME: Context-aware interactive prompting for user confirmations.
// ABOUTME: Provides readLine (races stdin vs context) and Confirm (y/N prompt).

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// readLine reads a single line from input, returning early if ctx is cancelled.
// On EOF (empty reader), returns ("", nil) so callers can treat it as a default.
// The reading goroutine may outlive the call on cancellation; this is acceptable
// for a CLI that is about to exit.
func readLine(ctx context.Context, input io.Reader) (string, error) {
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		if scanner.Scan() {
			ch <- scanner.Text()
		} else {
			ch <- ""
		}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line := <-ch:
		return line, nil
	}
}

// Confirm prints a prompt and reads y/N from the reader.
// Returns true if the user answered "y" or "yes" (case-insensitive).
// Returns an error if the context is cancelled (e.g. Ctrl+C).
func Confirm(ctx context.Context, prompt string, input io.Reader, output io.Writer) (bool, error) {
	fmt.Fprint(output, prompt) //nolint:errcheck // best-effort output
	line, err := readLine(ctx, input)
	if err != nil {
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes", nil
}
