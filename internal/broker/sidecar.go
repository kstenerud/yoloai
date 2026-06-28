// ABOUTME: RunSidecar — the body of the out-of-process key-injector: reads its
// ABOUTME: config (with the secret) from stdin, hands the resolved listen addr back, serves.
package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/kstenerud/yoloai/internal/credential"
)

// InjectVerb is the hidden argv[1] under which the yoloai binary runs as a
// credential-injector sidecar. The entrypoint (cmd/yoloai) dispatches it to
// RunSidecar before the normal CLI bootstrap — the sidecar runs with an empty
// env (no HOME/data dir) and reserves stdout for the address handshake, so it
// must bypass the layout/migration machinery the CLI sets up.
const InjectVerb = "__inject"

// Binding-kind tags carried in SidecarConfig (the wire form of a credential
// binding). Phase 1 supports only the two static-source Apply variants; the
// reserved request-signer/minting variants are not brokerable through the
// sidecar and are rejected by buildBindings.
const (
	KindHeaderSet = "header-set"
	KindBasicAuth = "basic-auth"
)

// BindingConfig is the wire form of one credential binding handed to the sidecar.
// It carries the secret, so it crosses only via the child's stdin and is never
// persisted (see InjectorRecord, which omits it).
type BindingConfig struct {
	Destination string `json:"destination"`
	Kind        string `json:"kind"`
	Header      string `json:"header,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	Username    string `json:"username,omitempty"`
	Secret      string `json:"secret"`
}

// SidecarConfig is the full configuration the host writes to the sidecar's
// stdin. It carries the real credentials and must never be persisted or placed
// in argv/env.
type SidecarConfig struct {
	UpstreamURL  string          `json:"upstream_url"`
	BindAddr     string          `json:"bind_addr"`
	StripHeaders []string        `json:"strip_headers,omitempty"`
	Bindings     []BindingConfig `json:"bindings"`
}

// Handshake is the one line the sidecar writes back (to stdout) once it has
// bound its listener: the resolved address the container reaches it at.
type Handshake struct {
	Addr string `json:"addr"`
}

// sidecarReadHeaderTimeout bounds the header read so a stuck client can't pin a
// connection open forever. There is deliberately no write timeout: brokered LLM
// responses are long-lived SSE streams.
const sidecarReadHeaderTimeout = 30 * time.Second

// RunSidecar is the entry point of the out-of-process injector (the `__inject`
// subcommand). It decodes a SidecarConfig from stdin, builds the injector, binds
// its listener, writes the resolved address to handshake, and serves until ctx
// is cancelled. The secret arrives via stdin (never argv/env) and stays in
// memory for the process's lifetime.
func RunSidecar(ctx context.Context, stdin io.Reader, handshake io.Writer) error {
	var cfg SidecarConfig
	if err := json.NewDecoder(stdin).Decode(&cfg); err != nil {
		return fmt.Errorf("broker: read sidecar config: %w", err)
	}

	up, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return fmt.Errorf("broker: parse upstream URL %q: %w", cfg.UpstreamURL, err)
	}
	bindings, err := buildBindings(cfg.Bindings)
	if err != nil {
		return err
	}
	inj, err := NewInjector(Upstream{URL: up, Bindings: bindings, StripHeaders: cfg.StripHeaders})
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.BindAddr)
	if err != nil {
		return fmt.Errorf("broker: listen on %q: %w", cfg.BindAddr, err)
	}

	if err := json.NewEncoder(handshake).Encode(Handshake{Addr: ln.Addr().String()}); err != nil {
		_ = ln.Close()
		return fmt.Errorf("broker: write handshake: %w", err)
	}

	srv := &http.Server{
		Handler:           inj,
		ReadHeaderTimeout: sidecarReadHeaderTimeout,
		// Diagnostics must not reach stdout (reserved for the handshake) — discard.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("broker: serve: %w", err)
	}
	return nil
}

// buildBindings converts the wire bindings into credential.CredentialBindings
// over Static sources. Unsupported kinds (the reserved request-signer/minting
// variants) are rejected rather than silently dropped.
func buildBindings(cfgs []BindingConfig) ([]credential.CredentialBinding, error) {
	out := make([]credential.CredentialBinding, 0, len(cfgs))
	for _, c := range cfgs {
		var apply credential.Apply
		switch c.Kind {
		case KindHeaderSet:
			apply = credential.HeaderSet{Header: c.Header, Prefix: c.Prefix}
		case KindBasicAuth:
			apply = credential.BasicAuth{Username: c.Username}
		default:
			return nil, fmt.Errorf("broker: unsupported binding kind %q (Phase 1 supports %q and %q)",
				c.Kind, KindHeaderSet, KindBasicAuth)
		}
		out = append(out, credential.CredentialBinding{
			Destination: credential.Destination(c.Destination),
			Apply:       apply,
			Source:      credential.Static(c.Secret),
		})
	}
	return out, nil
}
