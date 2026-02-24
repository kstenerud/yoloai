package sandbox

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Confirm prints a prompt and reads y/N from the reader.
// Returns true if the user answered "y" or "yes" (case-insensitive).
func Confirm(prompt string, input io.Reader, output io.Writer) bool {
	fmt.Fprint(output, prompt) //nolint:errcheck // best-effort output
	scanner := bufio.NewScanner(input)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
	return false
}
