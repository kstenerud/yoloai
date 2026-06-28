// ABOUTME: Black-box tests for Destination matching and ApplyTo's binding
// ABOUTME: selection (host/port/case, nil-skip, only-matching-destination).
package credential_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/credential"
)

func TestDestinationMatches(t *testing.T) {
	const dest = credential.Destination("api.anthropic.com")

	assert.True(t, dest.Matches("api.anthropic.com"))
	assert.True(t, dest.Matches("API.Anthropic.COM"), "case-insensitive")
	assert.True(t, dest.Matches("api.anthropic.com:443"), "port-insensitive")
	assert.False(t, dest.Matches("api.openai.com"))
	assert.False(t, dest.Matches(""))

	assert.False(t, credential.Destination("").Matches("api.anthropic.com"),
		"empty Destination matches nothing")
}

func TestApplyTo_OnlyMatchingDestination(t *testing.T) {
	req := newRequest(t, "api.anthropic.com")
	bindings := []credential.CredentialBinding{
		{
			Destination: "api.openai.com",
			Apply:       credential.HeaderSet{Header: "x-other"},
			Source:      credential.Static("nope"),
		},
		{
			Destination: "api.anthropic.com",
			Apply:       credential.HeaderSet{Header: "x-api-key"},
			Source:      credential.Static("yes"),
		},
	}

	require.NoError(t, credential.ApplyTo(context.Background(), req, bindings))
	assert.Equal(t, "yes", req.Header.Get("x-api-key"))
	assert.Empty(t, req.Header.Get("x-other"), "non-matching binding must not apply")
}

func TestApplyTo_SkipsUnconfiguredBindings(t *testing.T) {
	req := newRequest(t, "api.anthropic.com")
	bindings := []credential.CredentialBinding{
		{Destination: "api.anthropic.com", Apply: nil, Source: credential.Static("x")},
		{Destination: "api.anthropic.com", Apply: credential.HeaderSet{Header: "x-api-key"}, Source: nil},
	}

	require.NoError(t, credential.ApplyTo(context.Background(), req, bindings),
		"nil Apply/Source bindings are skipped, not errors")
	assert.Empty(t, req.Header.Get("x-api-key"))
}

func TestApplyTo_MatchesURLHost(t *testing.T) {
	// A forwarded/client request carries the host on the URL, not the Host header.
	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/v1/messages", nil)
	require.NoError(t, err)
	req.Host = ""

	bindings := []credential.CredentialBinding{{
		Destination: "api.anthropic.com",
		Apply:       credential.HeaderSet{Header: "x-api-key"},
		Source:      credential.Static("from-url-host"),
	}}

	require.NoError(t, credential.ApplyTo(context.Background(), req, bindings))
	assert.Equal(t, "from-url-host", req.Header.Get("x-api-key"))
}
