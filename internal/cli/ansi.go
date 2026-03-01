package cli

// ABOUTME: ANSI escape sequence stripping for readable log output.
// ABOUTME: Used by `yoloai log` to clean tmux pipe-pane capture.

import (
	"bufio"
	"io"
	"regexp"
)

// ansiPattern matches ANSI escape sequences: CSI (colors, cursor, erase),
// OSC (title setting), and character set selection.
var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[A-Za-z]|\][^\x07]*\x07|[()][AB012])`)

// stripANSI copies src to dst with ANSI escape sequences removed.
// It processes line-by-line to avoid partial-match issues at buffer boundaries.
func stripANSI(dst io.Writer, src io.Reader) error {
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		line := ansiPattern.ReplaceAll(scanner.Bytes(), nil)
		if _, err := dst.Write(line); err != nil {
			return err
		}
		if _, err := dst.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return scanner.Err()
}
