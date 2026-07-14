// ABOUTME: Unit tests for the key-injector: placeholder->real-key swap, placeholder
// ABOUTME: stripping, SSE pass-through, host-mismatch no-op, and injection-failure 502.
package broker_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/broker"
	"github.com/kstenerud/yoloai/internal/credential"
)

// recordingUpstream is a fake real-upstream that records the auth headers it
// received and replies with a canned SSE stream.
type recordingUpstream struct {
	server    *httptest.Server
	gotKey    string
	gotAuth   string
	gotGoogle string
	hits      int
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	t.Helper()
	u := &recordingUpstream{}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits++
		u.gotKey = r.Header.Get("x-api-key")
		u.gotAuth = r.Header.Get("Authorization")
		u.gotGoogle = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"type\":\"message_start\"}\n\ndata: hello\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(u.server.Close)
	return u
}

func (u *recordingUpstream) host(t *testing.T) string {
	t.Helper()
	parsed, err := url.Parse(u.server.URL)
	require.NoError(t, err)
	return parsed.Hostname()
}

// startInjector fronts up with an injector and returns its base URL.
func startInjector(t *testing.T, up broker.Upstream) string {
	t.Helper()
	inj, err := broker.NewInjector(up)
	require.NoError(t, err)
	front := httptest.NewServer(inj)
	t.Cleanup(front.Close)
	return front.URL
}

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestInjector_SwapsPlaceholderForRealKey(t *testing.T) {
	upstream := newRecordingUpstream(t)

	front := startInjector(t, broker.Upstream{
		URL: parseURL(t, upstream.server.URL),
		Bindings: []credential.CredentialBinding{{
			Destination: credential.Destination(upstream.host(t)),
			Apply:       credential.HeaderSet{Header: "x-api-key"},
			Source:      credential.Static("sk-ant-real"),
		}},
		StripHeaders: []string{"Authorization"},
	})

	req, err := http.NewRequest(http.MethodPost, front+"/v1/messages", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dummy-broker-token") // the agent's placeholder

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "sk-ant-real", upstream.gotKey, "real key injected")
	assert.Empty(t, upstream.gotAuth, "placeholder Authorization stripped")
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"), "stream content-type preserved")
	assert.Contains(t, string(body), "data: hello", "upstream stream rendered back to the agent")
}

func TestInjector_BearerPlaceholderReplaced(t *testing.T) {
	upstream := newRecordingUpstream(t)

	front := startInjector(t, broker.Upstream{
		URL: parseURL(t, upstream.server.URL),
		Bindings: []credential.CredentialBinding{{
			Destination: credential.Destination(upstream.host(t)),
			Apply:       credential.HeaderSet{Header: "Authorization", Prefix: "Bearer "},
			Source:      credential.Static("real-oauth-token"),
		}},
	})

	req, err := http.NewRequest(http.MethodPost, front+"/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dummy")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, "Bearer real-oauth-token", upstream.gotAuth, "bearer placeholder overwritten with real token")
}

// TestInjector_GeminiHeaderPlaceholderSwapped proves the D115 generalization end
// to end for a non-Authorization placeholder header: Gemini carries its placeholder
// in x-goog-api-key (not Authorization), so the injector must verify the token in
// that header, strip it, and inject the real key into the same header. This is the
// exact wiring buildInjectorSpec produces for the gemini agent.
func TestInjector_GeminiHeaderPlaceholderSwapped(t *testing.T) {
	upstream := newRecordingUpstream(t)

	front := startInjector(t, broker.Upstream{ //nolint:gosec // G101 false positive: test placeholder token, not a real credential
		URL: parseURL(t, upstream.server.URL),
		Bindings: []credential.CredentialBinding{{
			Destination: credential.Destination(upstream.host(t)),
			Apply:       credential.HeaderSet{Header: "x-goog-api-key"},
			Source:      credential.Static("real-gemini-key"),
		}},
		StripHeaders:  []string{"x-goog-api-key"},
		ExpectedToken: "per-sandbox-secret",
	})

	// Wrong token in x-goog-api-key → 403, never forwarded.
	bad, err := http.NewRequest(http.MethodPost, front+"/v1beta/models/gemini-2.5-pro:generateContent", strings.NewReader("{}"))
	require.NoError(t, err)
	bad.Header.Set("x-goog-api-key", "not-the-token")
	badResp, err := http.DefaultClient.Do(bad)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, badResp.Body)
	_ = badResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, badResp.StatusCode)
	assert.Equal(t, 0, upstream.hits, "a request with the wrong placeholder must not reach the upstream")

	// Correct placeholder → forwarded, placeholder stripped, real key injected.
	good, err := http.NewRequest(http.MethodPost, front+"/v1beta/models/gemini-2.5-pro:generateContent", strings.NewReader("{}"))
	require.NoError(t, err)
	good.Header.Set("x-goog-api-key", "per-sandbox-secret")
	goodResp, err := http.DefaultClient.Do(good)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, goodResp.Body)
	_ = goodResp.Body.Close()
	assert.Equal(t, http.StatusOK, goodResp.StatusCode)
	assert.Equal(t, "real-gemini-key", upstream.gotGoogle, "real key injected into x-goog-api-key")
	assert.Empty(t, upstream.gotAuth, "no stray Authorization header")
}

// TestInjector_RejectsWrongPlaceholderToken is the H3 fix: with a per-sandbox
// ExpectedToken set, a request that does not present it (e.g. a co-resident
// container on the shared bridge probing the injector port) is rejected with 403
// and never reaches the upstream, so the victim's credential is never injected.
// The correct token is accepted and the real credential injected as usual.
func TestInjector_RejectsWrongPlaceholderToken(t *testing.T) {
	upstream := newRecordingUpstream(t)

	front := startInjector(t, broker.Upstream{ //nolint:gosec // G101 false positive: test placeholder token, not a real credential
		URL: parseURL(t, upstream.server.URL),
		Bindings: []credential.CredentialBinding{{
			Destination: credential.Destination(upstream.host(t)),
			Apply:       credential.HeaderSet{Header: "x-api-key"},
			Source:      credential.Static("sk-ant-real"),
		}},
		StripHeaders:  []string{"Authorization"},
		ExpectedToken: "per-sandbox-secret",
	})

	// Wrong token → 403, never forwarded, no credential injected.
	bad, err := http.NewRequest(http.MethodPost, front+"/v1/messages", strings.NewReader("{}"))
	require.NoError(t, err)
	bad.Header.Set("Authorization", "Bearer not-the-token")
	badResp, err := http.DefaultClient.Do(bad)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, badResp.Body)
	_ = badResp.Body.Close()
	assert.Equal(t, http.StatusForbidden, badResp.StatusCode)
	assert.Equal(t, 0, upstream.hits, "a request with the wrong token must not reach the upstream")

	// Correct token (with the Bearer prefix the agent sends) → forwarded + injected.
	good, err := http.NewRequest(http.MethodPost, front+"/v1/messages", strings.NewReader("{}"))
	require.NoError(t, err)
	good.Header.Set("Authorization", "Bearer per-sandbox-secret")
	goodResp, err := http.DefaultClient.Do(good)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, goodResp.Body)
	_ = goodResp.Body.Close()
	assert.Equal(t, http.StatusOK, goodResp.StatusCode)
	assert.Equal(t, 1, upstream.hits)
	assert.Equal(t, "sk-ant-real", upstream.gotKey, "real key injected for the authenticated request")
}

func TestInjector_NoBindingForHostLeavesRequestUnauthenticated(t *testing.T) {
	upstream := newRecordingUpstream(t)

	front := startInjector(t, broker.Upstream{
		URL: parseURL(t, upstream.server.URL),
		// Destination names a different host, so nothing matches the rewritten request.
		Bindings: []credential.CredentialBinding{{
			Destination: "api.elsewhere.example",
			Apply:       credential.HeaderSet{Header: "x-api-key"},
			Source:      credential.Static("unused"),
		}},
	})

	req, err := http.NewRequest(http.MethodPost, front+"/v1/messages", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, 1, upstream.hits)
	assert.Empty(t, upstream.gotKey, "binding for a non-matching host must not inject")
}

func TestInjector_InjectionFailureIs502AndDoesNotForward(t *testing.T) {
	upstream := newRecordingUpstream(t)

	front := startInjector(t, broker.Upstream{
		URL: parseURL(t, upstream.server.URL),
		Bindings: []credential.CredentialBinding{{
			Destination: credential.Destination(upstream.host(t)),
			Apply:       credential.HeaderSet{Header: "x-api-key"},
			Source: credential.Refreshing(func(context.Context) (string, time.Time, error) {
				return "", time.Time{}, errors.New("token exchange unavailable")
			}),
		}},
	})

	req, err := http.NewRequest(http.MethodPost, front+"/v1/messages", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode, "credential failure surfaces as 502")
	assert.Equal(t, 0, upstream.hits, "request must not reach the upstream without the real credential")
}

func TestNewInjector_RejectsBadUpstream(t *testing.T) {
	_, err := broker.NewInjector(broker.Upstream{URL: nil})
	require.Error(t, err)

	_, err = broker.NewInjector(broker.Upstream{URL: &url.URL{Host: "api.anthropic.com"}}) // no scheme
	require.Error(t, err)

	_, err = broker.NewInjector(broker.Upstream{URL: &url.URL{Scheme: "https"}}) // no host
	require.Error(t, err)
}

// TestInjector_WebSocketUpgradeInjectsCredential verifies the injector proxies a
// WebSocket upgrade AND injects the real credential on the handshake. Codex 0.144
// hits {base_url}/responses over a WebSocket (with an HTTPS fallback), so the
// injector must forward the upgrade to the upstream with the placeholder swapped
// for the real key, not just plain POSTs.
func TestInjector_WebSocketUpgradeInjectsCredential(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		// Minimal 101 so httputil.ReverseProxy switches to tunnel (WS) mode.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("upstream not hijackable")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
	}))
	defer upstream.Close()

	upURL := parseURL(t, upstream.URL)
	front := startInjector(t, broker.Upstream{ //nolint:gosec // G101 false positive: test placeholder token, not a real credential
		URL: upURL,
		Bindings: []credential.CredentialBinding{{
			Destination: credential.Destination(upURL.Hostname()),
			Apply:       credential.HeaderSet{Header: "Authorization", Prefix: "Bearer "},
			Source:      credential.Static("real-openai-key"),
		}},
		StripHeaders:  []string{"Authorization"},
		ExpectedToken: "per-sandbox-secret",
	})

	conn, err := net.Dial("tcp", parseURL(t, front).Host)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("GET /v1/responses HTTP/1.1\r\n" +
		"Host: " + parseURL(t, front).Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Authorization: Bearer per-sandbox-secret\r\n\r\n"))
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode, "injector forwards the WS upgrade and relays upstream's 101")
	assert.Equal(t, "Bearer real-openai-key", gotAuth, "real credential injected into the WS handshake; placeholder stripped")
}
