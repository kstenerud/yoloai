// ABOUTME: Black-box tests for the CredentialSource variants: static, refreshing
// ABOUTME: (first-fetch/cache/error), and the reserved minting stub.
package credential_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/credential"
)

func TestStaticSource(t *testing.T) {
	src := credential.Static("sk-ant-real")
	v, err := src.Value(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "sk-ant-real", v)
}

func TestRefreshingSource_FetchesOnceAndCaches(t *testing.T) {
	var calls int
	src := credential.Refreshing(func(context.Context) (string, time.Time, error) {
		calls++
		return "token-1", time.Now().Add(time.Hour), nil
	})

	for range 3 {
		v, err := src.Value(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "token-1", v)
	}
	assert.Equal(t, 1, calls, "a still-valid token is served from cache")
}

func TestRefreshingSource_ZeroExpiryNeverRefetches(t *testing.T) {
	var calls int
	src := credential.Refreshing(func(context.Context) (string, time.Time, error) {
		calls++
		return "static-ish", time.Time{}, nil // zero expiry == never expires
	})

	_, err := src.Value(context.Background())
	require.NoError(t, err)
	_, err = src.Value(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRefreshingSource_FetchErrorPropagates(t *testing.T) {
	sentinel := errors.New("exchange rejected")
	src := credential.Refreshing(func(context.Context) (string, time.Time, error) {
		return "", time.Time{}, sentinel
	})

	_, err := src.Value(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

func TestMintingSource_Reserved(t *testing.T) {
	src := credential.MintingSource{Scheme: "github-app"}
	_, err := src.Value(context.Background())
	assert.ErrorIs(t, err, credential.ErrNotImplemented)
}
