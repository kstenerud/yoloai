package config

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// safeASCII marks ASCII bytes that do NOT need caret encoding.
// Safe: alphanumeric, hyphen, underscore, backtick, braces, dot, tilde.
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
	safeASCII['.'] = true
	safeASCII['~'] = true
}

// encodeShortcuts maps characters to their single-letter caret shortcut codes.
var encodeShortcuts = map[rune]byte{
	'^': '^', ' ': '_', '=': '-', '+': '`',
	'(': '{', ')': '}', '>': 'g', '#': 'h',
	'!': 'i', '\'': 'j', ':': 'k', '<': 'l',
	'%': 'm', '&': 'n', '@': 'o', '|': 'p',
	'?': 'q', '\\': 'r', '/': 's', '*': 't',
	'"': 'u', '$': 'v',
}

// decodeShortcuts maps single-letter caret shortcut codes back to characters.
// Both cases are accepted for letters.
var decodeShortcuts = map[byte]rune{
	'^': '^', '_': ' ', '-': '=', '`': '+',
	'{': '(', '}': ')',
	'g': '>', 'G': '>', 'h': '#', 'H': '#',
	'i': '!', 'I': '!', 'j': '\'', 'J': '\'',
	'k': ':', 'K': ':', 'l': '<', 'L': '<',
	'm': '%', 'M': '%', 'n': '&', 'N': '&',
	'o': '@', 'O': '@', 'p': '|', 'P': '|',
	'q': '?', 'Q': '?', 'r': '\\', 'R': '\\',
	's': '/', 'S': '/', 't': '*', 'T': '*',
	'u': '"', 'U': '"', 'v': '$', 'V': '$',
}

// encodeRune encodes a single rune using the shortest caret representation.
func encodeRune(builder *strings.Builder, r rune) {
	cp := uint32(r) //nolint:gosec // rune values are always valid Unicode codepoints
	switch {
	case cp <= 0xFF:
		fmt.Fprintf(builder, "^%02X", cp)
	case cp <= 0xFFF:
		fmt.Fprintf(builder, "^w%03X", cp)
	case cp <= 0xFFFF:
		fmt.Fprintf(builder, "^x%04X", cp)
	case cp <= 0xFFFFF:
		fmt.Fprintf(builder, "^y%05X", cp)
	default:
		fmt.Fprintf(builder, "^z%06X", cp)
	}
}

// EncodePath encodes a host path using the caret encoding spec
// (https://github.com/kstenerud/caret-encoding) for use as a
// filesystem-safe directory name.
func EncodePath(hostPath string) string {
	var builder strings.Builder
	builder.Grow(len(hostPath))

	for i, r := range hostPath {
		// Encode trailing dots in path components — Windows strips them from filenames.
		if r == '.' && (i+1 >= len(hostPath) || hostPath[i+1] == '/') {
			encodeRune(&builder, r)
			continue
		}
		if r < 128 && safeASCII[byte(r)] { //nolint:gosec // r < 128 guarantees safe conversion
			builder.WriteRune(r)
		} else if sc, ok := encodeShortcuts[r]; ok {
			builder.WriteByte('^')
			builder.WriteByte(sc)
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

		modifier := encoded[i]

		// Check shortcuts first.
		if r, ok := decodeShortcuts[modifier]; ok {
			builder.WriteRune(r)
			i++
			continue
		}

		// Check width modifiers (w/x/y/z).
		hexDigits := 2
		switch modifier {
		case 'w', 'W':
			hexDigits = 3
			i++
		case 'x', 'X':
			hexDigits = 4
			i++
		case 'y', 'Y':
			hexDigits = 5
			i++
		case 'z', 'Z':
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
