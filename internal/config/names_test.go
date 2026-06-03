package config

// ABOUTME: Tests for the parsed SandboxName / PrincipalSegment boundary types.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSandboxName(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		for _, name := range []string{
			"myproject",
			"my-project",
			"my_project",
			"my.project",
			"Project123",
			"a",
			"1test",
			"a.b-c_d",
			strings.Repeat("a", MaxNameLength),
		} {
			got, err := ParseSandboxName(name)
			assert.NoError(t, err, "expected %q to be valid", name)
			assert.Equal(t, SandboxName(name), got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, err := ParseSandboxName("")
		assert.ErrorContains(t, err, "required")
	})

	t.Run("path-like", func(t *testing.T) {
		for _, name := range []string{"/home/user/p", `\Users\foo`} {
			_, err := ParseSandboxName(name)
			assert.ErrorContains(t, err, "looks like a path")
		}
	})

	t.Run("too long", func(t *testing.T) {
		_, err := ParseSandboxName(strings.Repeat("a", MaxNameLength+1))
		assert.ErrorContains(t, err, "at most")
	})

	// DF16: leading/trailing/doubled separators that containerd rejects.
	t.Run("invalid grammar", func(t *testing.T) {
		for _, name := range []string{
			"-leading", "_leading", ".leading",
			"trailing-", "trailing_", "trailing.",
			"a..b", "x__y", "a--b",
			"has space", "has/slash", "has:colon", ".", "..",
		} {
			_, err := ParseSandboxName(name)
			assert.Error(t, err, "expected %q to be invalid", name)
		}
	})
}

func TestParsePrincipalSegment(t *testing.T) {
	t.Run("empty is the default sentinel", func(t *testing.T) {
		got, err := ParsePrincipalSegment("")
		assert.NoError(t, err)
		assert.Equal(t, PrincipalSegment(""), got)
	})

	t.Run("valid", func(t *testing.T) {
		for _, p := range []string{"a", "acme", "Globex9", strings.Repeat("a", MaxPrincipalLength)} {
			got, err := ParsePrincipalSegment(p)
			assert.NoError(t, err, "expected %q to be valid", p)
			assert.Equal(t, PrincipalSegment(p), got)
		}
	})

	t.Run("too long", func(t *testing.T) {
		_, err := ParsePrincipalSegment(strings.Repeat("a", MaxPrincipalLength+1))
		assert.ErrorContains(t, err, "at most")
	})

	t.Run("invalid charset (no separators allowed)", func(t *testing.T) {
		for _, p := range []string{"a-b", "a_b", "a.b", "has space", "a/b"} {
			_, err := ParsePrincipalSegment(p)
			assert.Error(t, err, "expected %q to be invalid", p)
		}
	})
}
