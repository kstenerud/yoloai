// ABOUTME: The always-on key-injector (D105 layer 1): a per-sandbox fixed-upstream
// ABOUTME: reverse proxy that swaps the agent's placeholder credential for the real one.
package broker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/kstenerud/yoloai/internal/credential"
)

// Upstream describes the real endpoint the injector forwards to and how it
// authenticates. The agent is configured (via base_url) to send its requests to
// the injector carrying a placeholder credential; the injector forwards them to
// URL with the real credential injected.
type Upstream struct {
	// URL is the real upstream base (scheme + host), e.g.
	// https://api.anthropic.com. Scheme and host route the request; the agent's
	// request path is preserved (any base path in URL is prepended).
	URL *url.URL

	// Bindings inject the real credentials. Each binding's Destination must match
	// URL's host. The default metered-key case is a single HeaderSet binding over
	// a StaticSource (the real x-api-key).
	Bindings []credential.CredentialBinding

	// StripHeaders names inbound headers removed before injection — the
	// placeholder-credential carriers (e.g. "Authorization" when the agent sends
	// ANTHROPIC_AUTH_TOKEN). Removing them ensures a dummy token never reaches the
	// upstream alongside the injected real credential.
	StripHeaders []string
}

// Injector is the always-on key-injector (D105 layer 1): a small reverse proxy
// with a single fixed upstream. It is the credential-protection primitive — the
// real key never enters the sandbox because the agent never holds it, and the
// injector always forwards to the configured upstream regardless of what the
// agent asks (the unforgeable-reach / IMDSv2 property). General, non-LLM traffic
// is out of scope: the injector fronts only the one configured endpoint;
// everything else the agent does goes direct.
//
// Injector implements http.Handler; the caller serves it on a loopback listener
// (one per sandbox) and points the agent's base_url at that address.
type Injector struct {
	upstream Upstream
	proxy    *httputil.ReverseProxy
}

// NewInjector builds an injector for one upstream. It errors if the upstream URL
// lacks a scheme or host.
func NewInjector(up Upstream) (*Injector, error) {
	if up.URL == nil || up.URL.Scheme == "" || up.URL.Host == "" {
		return nil, fmt.Errorf("broker: upstream URL needs a scheme and host")
	}
	inj := &Injector{upstream: up}
	inj.proxy = &httputil.ReverseProxy{
		Rewrite: inj.rewrite,
		// Flush each chunk immediately so streamed (SSE) responses reach the
		// agent as they arrive rather than being buffered.
		FlushInterval: -1,
		Transport:     injectErrTransport{base: http.DefaultTransport},
	}
	return inj, nil
}

func (inj *Injector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	inj.proxy.ServeHTTP(w, r)
}

// rewrite routes the outbound request to the real upstream and injects the real
// credential in place of the agent's placeholder.
func (inj *Injector) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(inj.upstream.URL)
	// Send the upstream's own Host (for vhost routing + TLS SNI), not the
	// loopback base_url host the agent dialed.
	pr.Out.Host = inj.upstream.URL.Host

	for _, h := range inj.upstream.StripHeaders {
		pr.Out.Header.Del(h)
	}

	if err := credential.ApplyTo(pr.Out.Context(), pr.Out, inj.upstream.Bindings); err != nil {
		// Rewrite cannot fail the request directly; carry the error to the
		// transport so it becomes a 502 rather than forwarding the placeholder.
		ctx := context.WithValue(pr.Out.Context(), injectErrKey{}, err)
		*pr.Out = *pr.Out.WithContext(ctx)
	}
}

type injectErrKey struct{}

// injectErrTransport aborts a request that carries an injection error (stashed by
// rewrite) instead of forwarding it, so a credential-resolution failure surfaces
// as a 502 and never leaks the request upstream without the real credential.
type injectErrTransport struct{ base http.RoundTripper }

func (t injectErrTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err, ok := r.Context().Value(injectErrKey{}).(error); ok && err != nil {
		return nil, fmt.Errorf("broker: credential injection failed: %w", err)
	}
	return t.base.RoundTrip(r)
}
