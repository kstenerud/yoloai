package cli

// ABOUTME: ANSI escape sequence and control character stripping for readable log output.
// ABOUTME: Used by `yoloai log` to clean tmux pipe-pane capture.

import (
	"bufio"
	"io"
	"regexp"
)

// ansiPattern matches ANSI escape sequences: CSI (colors, cursor, erase),
// OSC (title setting), and character set selection.
var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[A-Za-z]|\][^\x07]*\x07|[()][AB012])`)

// controlPattern matches problematic control characters: C0 controls except
// tab (0x09), plus DEL (0x7F). Newlines (0x0A) are handled by the line
// scanner and carriage returns (0x0D) are stripped to prevent overwrite
// artifacts in log display.
var controlPattern = regexp.MustCompile(`[\x00-\x08\x0b-\x0c\x0d-\x1a\x7f]`)

// stripANSI copies src to dst with ANSI escape sequences and problematic
// control characters removed. It processes line-by-line to avoid
// partial-match issues at buffer boundaries.
func stripANSI(dst io.Writer, src io.Reader) error {
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		line := ansiPattern.ReplaceAll(scanner.Bytes(), nil)
		line = controlPattern.ReplaceAll(line, nil)
		if _, err := dst.Write(line); err != nil {
			return err
		}
		if _, err := dst.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return scanner.Err()
}
