package cli

// ABOUTME: ANSI escape sequence and control character stripping for readable log output.
// ABOUTME: Used by `yoloai log` to clean tmux pipe-pane capture.

import (
	"bufio"
	"io"
	"regexp"
)

// ansiPattern matches VT100/ANSI escape sequences using grammar-based rules,
// without needing to understand individual sequence semantics:
//   - CSI: ESC [ + param bytes (0x30-0x3F) + intermediate bytes (0x20-0x2F) + final byte (0x40-0x7E)
//   - OSC: ESC ] + string + BEL or ST (ESC \)
//   - nF:  ESC + intermediate byte(s) (0x20-0x2F) + final byte (0x30-0x7E) — e.g. character set designation
//   - 2-char: ESC + any single byte 0x30-0x7E (Fp/Fe/Fs — ESC 7, ESC M, ESC c, etc.)
//
// DCS/APC/SOS/PM sequences are not matched — they span lines and are vanishingly
// rare in agent output.
var ansiPattern = regexp.MustCompile(`\x1b(?:` +
	`\[[0-?]*[ -/]*[@-~]` + // CSI
	`|\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC
	`|[ -/][0-~]` + // nF (character set designation etc.)
	`|[0-~]` + // all other 2-char sequences
	`)`)

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
