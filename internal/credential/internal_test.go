// ABOUTME: White-box tests needing package internals: clock-driven refresh
// ABOUTME: expiry, signer-runs-last ordering, and single-flight-ish concurrency.
package credential

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshingSource_RefetchesAfterExpiry(t *testing.T) {
	now := time.Unix(1_000, 0)
	var calls int
	src := Refreshing(func(context.Context) (string, time.Time, error) {
		calls++
		// Each token is valid for 10 minutes from the current fake clock; `now`
		// is captured by reference so it tracks the test's clock mutations.
		return fmt.Sprintf("token-%d", calls), now.Add(10 * time.Minute), nil
	}, WithRefreshSkew(time.Minute))
	src.clock = func() time.Time { return now }

	v, err := src.Value(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token-1", v)
	assert.Equal(t, 1, calls)

	// Still inside the validity window (minus skew): served from cache.
	now = now.Add(5 * time.Minute)
	v, err = src.Value(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token-1", v)
	assert.Equal(t, 1, calls)

	// Past expiry-minus-skew: re-fetched.
	now = now.Add(5 * time.Minute)
	v, err = src.Value(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "token-2", v)
	assert.Equal(t, 2, calls)
}

// recordingApply is a test-only Apply that logs the order in which transforms
// run. It can satisfy the sealed Apply interface only because this test is in
// package credential.
type recordingApply struct {
	name string
	last bool
	log  *[]string
}

func (r recordingApply) applyTo(_ *http.Request, _ string) error {
	*r.log = append(*r.log, r.name)
	return nil
}
func (r recordingApply) runsLast() bool { return r.last }
func (recordingApply) isApply()         {}

func TestApplyTo_SignerRunsLast(t *testing.T) {
	var order []string
	req, err := http.NewRequest(http.MethodGet, "http://api.example.com/", nil)
	require.NoError(t, err)

	bindings := []CredentialBinding{
		// Declared signer-first to prove ordering is by runsLast, not position.
		{Destination: "api.example.com", Apply: recordingApply{name: "signer", last: true, log: &order}, Source: Static("k")},
		{Destination: "api.example.com", Apply: recordingApply{name: "header", last: false, log: &order}, Source: Static("k")},
	}

	require.NoError(t, ApplyTo(context.Background(), req, bindings))
	assert.Equal(t, []string{"header", "signer"}, order)
}

func TestRefreshingSource_ConcurrentValueFetchesOnce(t *testing.T) {
	var mu sync.Mutex
	var calls int
	src := Refreshing(func(context.Context) (string, time.Time, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return "token", time.Time{}, nil // never expires
	})

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			v, err := src.Value(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, "token", v)
		})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, calls, "Value serializes; a never-expiring token is fetched once")
}
