package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodePath_BasicPaths(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"absolute path", "/home/user/project", "^2Fhome^2Fuser^2Fproject"},
		{"tmp path", "/tmp/test", "^2Ftmp^2Ftest"},
		{"safe only", "simple", "simple"},
		{"empty string", "", ""},
		{"root", "/", "^2F"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, EncodePath(tt.input))
		})
	}
}

func TestEncodePath_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"space", "/my dir", "^2Fmy^20dir"},
		{"caret", "/foo^bar", "^2Ffoo^5Ebar"},
		{"colon", "/foo:bar", "^2Ffoo^3Abar"},
		{"dot", "/foo.bar", "^2Ffoo^2Ebar"},
		{"hash", "/foo#bar", "^2Ffoo^23bar"},
		{"at sign", "/foo@bar", "^2Ffoo^40bar"},
		{"backslash", `/foo\bar`, "^2Ffoo^5Cbar"},
		{"tilde", "/foo~bar", "^2Ffoo^7Ebar"},
		{"exclamation", "/foo!bar", "^2Ffoo^21bar"},
		{"question mark", "/foo?bar", "^2Ffoo^3Fbar"},
		{"multiple specials", "/a b/c:d", "^2Fa^20b^2Fc^3Ad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, EncodePath(tt.input))
		})
	}
}

func TestEncodePath_NonASCII(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"latin extended", "/données", "^2Fdonn^E9es"},
		{"polish", "/Łódź", "^2F^g141^F3d^g17A"},
		{"cjk", "/日本", "^2F^h65E5^h672C"},
		{"emoji", "/test/🎉", "^2Ftest^2F^i1F389"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, EncodePath(tt.input))
		})
	}
}

func TestDecodePath_RoundTrip(t *testing.T) {
	paths := []string{
		"/home/user/project",
		"/tmp/test",
		"simple",
		"",
		"/",
		"/my dir/with spaces",
		"/foo^bar",
		"/foo:bar/baz.txt",
		"/données",
		"/Łódź",
		"/日本",
		"/test/🎉",
		"/a!b@c#d$e%f",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			encoded := EncodePath(path)
			decoded, err := DecodePath(encoded)
			require.NoError(t, err)
			assert.Equal(t, path, decoded)
		})
	}
}

func TestDecodePath_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		expected string
	}{
		{"lowercase hex", "^2f", "/"},
		{"uppercase hex", "^2F", "/"},
		{"lowercase modifier", "^g141", "Ł"},
		{"uppercase modifier", "^G141", "Ł"},
		{"mixed case hex", "^h00e9", "é"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := DecodePath(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, decoded)
		})
	}
}

func TestDecodePath_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"truncated at caret", "^"},
		{"truncated hex", "^2"},
		{"invalid hex", "^ZZ"},
		{"truncated with modifier", "^g14"},
		{"truncated modifier only", "^g"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodePath(tt.input)
			assert.Error(t, err)
		})
	}
}

func TestDir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	expected := filepath.Join(home, ".yoloai", "sandboxes", "my-sandbox")
	assert.Equal(t, expected, Dir("my-sandbox"))
}

func TestWorkDir(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	expected := filepath.Join(home, ".yoloai", "sandboxes", "my-sandbox", "work", "^2Fhome^2Fuser^2Fproject")
	assert.Equal(t, expected, WorkDir("my-sandbox", "/home/user/project"))
}
