// ABOUTME: Host-path <-> on-disk-name encoding (EncodePath/DecodePath, escaping
// ABOUTME: non-ASCII/special chars, round-trips), sandbox name validation
// ABOUTME: (DF16 separator rules), and the sandbox-dir path-builder helpers.
package store

import (
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
		{"absolute path", "/home/user/project", "^shome^suser^sproject"},
		{"tmp path", "/tmp/test", "^stmp^stest"},
		{"safe only", "simple", "simple"},
		{"empty string", "", ""},
		{"root", "/", "^s"},
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
		{"space", "/my dir", "^smy^_dir"},
		{"caret", "/foo^bar", "^sfoo^^bar"},
		{"colon", "/foo:bar", "^sfoo^kbar"},
		{"dot mid-component", "/foo.bar", "^sfoo.bar"},
		{"dot end-of-component", "/foo./bar", "^sfoo^2E^sbar"},
		{"dot end-of-path", "/foo.", "^sfoo^2E"},
		{"double dot end", "/foo..", "^sfoo.^2E"},
		{"hash", "/foo#bar", "^sfoo^hbar"},
		{"at sign", "/foo@bar", "^sfoo^obar"},
		{"backslash", `/foo\bar`, "^sfoo^rbar"},
		{"tilde", "/foo~bar", "^sfoo~bar"},
		{"exclamation", "/foo!bar", "^sfoo^ibar"},
		{"question mark", "/foo?bar", "^sfoo^qbar"},
		{"multiple specials", "/a b/c:d", "^sa^_b^sc^kd"},
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
		{"latin extended", "/données", "^sdonn^E9es"},
		{"polish", "/Łódź", "^s^w141^F3d^w17A"},
		{"cjk", "/日本", "^s^x65E5^x672C"},
		{"emoji", "/test/🎉", "^stest^s^y1F389"},
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
		"/trailing./dots.",
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := DecodePath(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, decoded)
		})
	}
}

func TestDecodePath_Shortcuts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"caret", "^^", "^"},
		{"space", "^_", " "},
		{"equals", "^-", "="},
		{"plus", "^`", "+"},
		{"open paren", "^{", "("},
		{"close paren", "^}", ")"},
		{"slash lower", "^s", "/"},
		{"slash upper", "^S", "/"},
		{"colon lower", "^k", ":"},
		{"colon upper", "^K", ":"},
		{"at lower", "^o", "@"},
		{"at upper", "^O", "@"},
		{"backslash lower", "^r", "\\"},
		{"backslash upper", "^R", "\\"},
		{"exclamation lower", "^i", "!"},
		{"exclamation upper", "^I", "!"},
		{"question lower", "^q", "?"},
		{"question upper", "^Q", "?"},
		{"hash lower", "^h", "#"},
		{"hash upper", "^H", "#"},
		{"dollar lower", "^v", "$"},
		{"dollar upper", "^V", "$"},
		{"combined", "^shome^suser", "/home/user"},
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

	// DF16: the old loose regex accepted trailing/leading/doubled separators
	// that containerd's container-id grammar rejects at create time. The
	// tightened grammar (via config.ParseSandboxName) now rejects them.
	t.Run("trailing/doubled separators (DF16)", func(t *testing.T) {
		for _, name := range []string{
			"my-app-",
			"my.app.",
			"my_app_",
			"a..b",
			"x__y",
			"a--b",
		} {
			err := ValidateName(name)
			assert.Error(t, err, "expected %q to be invalid", name)
		}
	})
}

func TestWorkDir(t *testing.T) {
	sandboxDir := "/data/sandboxes/my-sandbox"
	expected := filepath.Join(sandboxDir, "work", "^shome^suser^sproject")
	assert.Equal(t, expected, WorkDir(sandboxDir, "/home/user/project"))
}

func TestInstanceName(t *testing.T) {
	assert.Equal(t, "yoloai-mybox", InstanceName("", "mybox"))
	assert.Equal(t, "yoloai-acme-mybox", InstanceName("acme", "mybox"))
}

func TestOverlayLowerDir(t *testing.T) {
	sandboxDir := "/data/sandboxes/my-sandbox"
	expected := filepath.Join(sandboxDir, "work", EncodePath("/home/user/project"), "lower")
	assert.Equal(t, expected, OverlayLowerDir(sandboxDir, "/home/user/project"))
}

func TestFilesDir(t *testing.T) {
	sandboxDir := "/data/sandboxes/my-sandbox"
	expected := filepath.Join(sandboxDir, "files")
	assert.Equal(t, expected, FilesDir(sandboxDir))
}
