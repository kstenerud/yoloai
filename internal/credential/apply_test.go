// ABOUTME: Black-box tests for the Apply variants exercised through ApplyTo:
// ABOUTME: header-set, basic-auth, and the reserved request-signer stub.
package credential_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/credential"
)

func newRequest(t *testing.T, host string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+host+"/v1/messages", nil)
	require.NoError(t, err)
	return req
}

func TestApplyTo_HeaderSetBearer(t *testing.T) {
	req := newRequest(t, "api.anthropic.com")
	bindings := []credential.CredentialBinding{{
		Destination: "api.anthropic.com",
		Apply:       credential.HeaderSet{Header: "Authorization", Prefix: "Bearer "},
		Source:      credential.Static("real-key"),
	}}

	require.NoError(t, credential.ApplyTo(context.Background(), req, bindings))
	assert.Equal(t, "Bearer real-key", req.Header.Get("Authorization"))
}

func TestApplyTo_HeaderSetRaw(t *testing.T) {
	req := newRequest(t, "api.anthropic.com")
	bindings := []credential.CredentialBinding{{
		Destination: "api.anthropic.com",
		Apply:       credential.HeaderSet{Header: "x-api-key"},
		Source:      credential.Static("sk-ant-real"),
	}}

	require.NoError(t, credential.ApplyTo(context.Background(), req, bindings))
	assert.Equal(t, "sk-ant-real", req.Header.Get("x-api-key"))
}

func TestApplyTo_HeaderSetEmptyHeaderErrors(t *testing.T) {
	req := newRequest(t, "api.anthropic.com")
	bindings := []credential.CredentialBinding{{
		Destination: "api.anthropic.com",
		Apply:       credential.HeaderSet{},
		Source:      credential.Static("x"),
	}}

	err := credential.ApplyTo(context.Background(), req, bindings)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty Header")
}

func TestApplyTo_BasicAuth(t *testing.T) {
	req := newRequest(t, "github.com")
	bindings := []credential.CredentialBinding{{
		Destination: "github.com",
		Apply:       credential.BasicAuth{Username: "x-access-token"},
		Source:      credential.Static("ghs-token"),
	}}

	require.NoError(t, credential.ApplyTo(context.Background(), req, bindings))

	user, pass, ok := req.BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "x-access-token", user)
	assert.Equal(t, "ghs-token", pass)

	// And it is the standard base64(user:pass) Authorization header.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs-token"))
	assert.Equal(t, want, req.Header.Get("Authorization"))
}

func TestApplyTo_RequestSignerReserved(t *testing.T) {
	req := newRequest(t, "bedrock.us-east-1.amazonaws.com")
	bindings := []credential.CredentialBinding{{
		Destination: "bedrock.us-east-1.amazonaws.com",
		Apply:       credential.RequestSigner{Scheme: "aws-sigv4"},
		Source:      credential.Static("signing-key"),
	}}

	err := credential.ApplyTo(context.Background(), req, bindings)
	require.Error(t, err)
	assert.ErrorIs(t, err, credential.ErrNotImplemented)
}

func TestApplyTo_SourceErrorPropagates(t *testing.T) {
	sentinel := errors.New("mint failed")
	req := newRequest(t, "api.anthropic.com")
	bindings := []credential.CredentialBinding{{
		Destination: "api.anthropic.com",
		Apply:       credential.HeaderSet{Header: "x-api-key"},
		Source: credential.Refreshing(func(context.Context) (string, time.Time, error) {
			return "", time.Time{}, sentinel
		}),
	}}

	err := credential.ApplyTo(context.Background(), req, bindings)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Empty(t, req.Header.Get("x-api-key"))
}
