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
		{"polish", "/Łódź", "^2F^w141^F3d^w17A"},
		{"cjk", "/日本", "^2F^x65E5^x672C"},
		{"emoji", "/test/🎉", "^2Ftest^2F^y1F389"},
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
		name     string
		input    string
		expected string
	}{
		{"lowercase hex", "^2f", "/"},
		{"uppercase hex", "^2F", "/"},
		{"lowercase modifier", "^w141", "Ł"},
		{"uppercase modifier", "^W141", "Ł"},
		{"mixed case hex", "^x00e9", "é"},
		{"old modifier g", "^g141", "Ł"},
		{"old modifier G", "^G141", "Ł"},
		{"old modifier h", "^h00e9", "é"},
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
		{"truncated with modifier", "^w14"},
		{"truncated modifier only", "^w"},
		{"truncated old modifier", "^g14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodePath(tt.input)
			assert.Error(t, err)
		})
	}
}

func TestValidateName(t *testing.T) {
	t.Run("valid names", func(t *testing.T) {
		for _, name := range []string{
			"myproject",
			"my-project",
			"my_project",
			"my.project",
			"Project123",
			"a",
			"1test",
		} {
			assert.NoError(t, ValidateName(name), "expected %q to be valid", name)
		}
	})

	t.Run("empty", func(t *testing.T) {
		err := ValidateName("")
		assert.EqualError(t, err, "sandbox name is required")
	})

	t.Run("too long", func(t *testing.T) {
		long := string(make([]byte, 57))
		for i := range long {
			long = long[:i] + "a" + long[i+1:]
		}
		err := ValidateName(long)
		assert.ErrorContains(t, err, "must be at most 56 characters")
	})

	t.Run("max length is ok", func(t *testing.T) {
		name := string(make([]byte, 56))
		for i := range name {
			name = name[:i] + "a" + name[i+1:]
		}
		assert.NoError(t, ValidateName(name))
	})

	t.Run("path-like names", func(t *testing.T) {
		for _, name := range []string{"/home/user/project", `\Users\foo`} {
			err := ValidateName(name)
			assert.ErrorContains(t, err, "looks like a path", "name: %q", name)
		}
	})

	t.Run("invalid characters", func(t *testing.T) {
		for _, name := range []string{
			".",
			"..",
			"-leading-dash",
			"has space",
			"has/slash",
			"has:colon",
			"_leading",
		} {
			err := ValidateName(name)
			assert.Error(t, err, "expected %q to be invalid", name)
		}
	})
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
