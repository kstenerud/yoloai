// Package sandbox implements sandbox lifecycle operations.
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// safeASCII marks ASCII bytes that do NOT need caret encoding.
// Safe: alphanumeric, hyphen, underscore, backtick, braces.
var safeASCII [128]bool

func init() {
	for c := byte('0'); c <= '9'; c++ {
		safeASCII[c] = true
	}
	for c := byte('A'); c <= 'Z'; c++ {
		safeASCII[c] = true
	}
	for c := byte('a'); c <= 'z'; c++ {
		safeASCII[c] = true
	}
	safeASCII['-'] = true
	safeASCII['_'] = true
	safeASCII['`'] = true
	safeASCII['{'] = true
	safeASCII['}'] = true
}

// encodeRune encodes a single rune using the shortest caret representation.
func encodeRune(builder *strings.Builder, r rune) {
	cp := uint32(r) //nolint:gosec // rune values are always valid Unicode codepoints
	switch {
	case cp <= 0xFF:
		fmt.Fprintf(builder, "^%02X", cp)
	case cp <= 0xFFF:
		fmt.Fprintf(builder, "^g%03X", cp)
	case cp <= 0xFFFF:
		fmt.Fprintf(builder, "^h%04X", cp)
	case cp <= 0xFFFFF:
		fmt.Fprintf(builder, "^i%05X", cp)
	default:
		fmt.Fprintf(builder, "^j%06X", cp)
	}
}

// EncodePath encodes a host path using the caret encoding spec
// (https://github.com/kstenerud/caret-encoding) for use as a
// filesystem-safe directory name.
func EncodePath(hostPath string) string {
	var builder strings.Builder
	builder.Grow(len(hostPath))

	for _, r := range hostPath {
		if r < 128 && safeASCII[byte(r)] { //nolint:gosec // r < 128 guarantees safe conversion
			builder.WriteRune(r)
		} else {
			encodeRune(&builder, r)
		}
	}

	return builder.String()
}

// DecodePath reverses caret encoding back to the original path.
func DecodePath(encoded string) (string, error) {
	var builder strings.Builder
	builder.Grow(len(encoded))

	i := 0
	for i < len(encoded) {
		if encoded[i] != '^' {
			r, size := utf8.DecodeRuneInString(encoded[i:])
			builder.WriteRune(r)
			i += size
			continue
		}

		// Found '^' — determine modifier and hex digit count.
		i++ // skip '^'
		if i >= len(encoded) {
			return "", fmt.Errorf("truncated caret sequence at end of string")
		}

		hexDigits := 2
		modifier := encoded[i]
		switch modifier {
		case 'g', 'G':
			hexDigits = 3
			i++
		case 'h', 'H':
			hexDigits = 4
			i++
		case 'i', 'I':
			hexDigits = 5
			i++
		case 'j', 'J':
			hexDigits = 6
			i++
		}

		if i+hexDigits > len(encoded) {
			return "", fmt.Errorf("truncated caret sequence: need %d hex digits at position %d", hexDigits, i)
		}

		hexStr := encoded[i : i+hexDigits]
		codepoint, err := parseHex(hexStr)
		if err != nil {
			return "", fmt.Errorf("invalid caret sequence at position %d: %w", i, err)
		}

		r := rune(codepoint) //nolint:gosec // validated by utf8.ValidRune below
		if !utf8.ValidRune(r) {
			return "", fmt.Errorf("invalid Unicode codepoint U+%X", codepoint)
		}

		builder.WriteRune(r)
		i += hexDigits
	}

	return builder.String(), nil
}

// parseHex parses a hexadecimal string into a uint32 codepoint.
func parseHex(s string) (uint32, error) {
	var result uint32
	for _, c := range s {
		result <<= 4
		switch {
		case c >= '0' && c <= '9':
			result |= uint32(c - '0') //nolint:gosec // c >= '0' guarantees non-negative
		case c >= 'a' && c <= 'f':
			result |= uint32(c-'a') + 10 //nolint:gosec // bounded subtraction
		case c >= 'A' && c <= 'F':
			result |= uint32(c-'A') + 10 //nolint:gosec // bounded subtraction
		default:
			return 0, fmt.Errorf("invalid hex digit %q", c)
		}
	}
	return result, nil
}

// ContainerName returns the Docker container name for a sandbox.
func ContainerName(name string) string {
	return "yoloai-" + name
}

// Dir returns the host-side state directory for a sandbox.
//
//	~/.yoloai/sandboxes/<name>/
func Dir(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yoloai", "sandboxes", name)
}

// RequireSandboxDir returns the sandbox directory path after verifying it exists.
func RequireSandboxDir(name string) (string, error) {
	dir := Dir(name)
	if _, err := os.Stat(dir); err != nil {
		return "", ErrSandboxNotFound
	}
	return dir, nil
}

// WorkDir returns the host-side work directory for a specific
// copy-mode mount within a sandbox.
//
//	~/.yoloai/sandboxes/<name>/work/<caret-encoded-path>/
func WorkDir(name string, hostPath string) string {
	return filepath.Join(Dir(name), "work", EncodePath(hostPath))
}
