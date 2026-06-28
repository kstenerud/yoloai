// ABOUTME: Black-box tests for RunSidecar: it binds, hands back its address, injects
// ABOUTME: the real key on requests, and rejects unsupported binding kinds.
package broker_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/broker"
)

func TestRunSidecar_BindsServesAndInjects(t *testing.T) {
	upstream := newRecordingUpstream(t)

	cfg := broker.SidecarConfig{
		UpstreamURL:  upstream.server.URL,
		BindAddr:     "127.0.0.1:0",
		StripHeaders: []string{"Authorization"},
		Bindings: []broker.BindingConfig{{
			Destination: upstream.host(t),
			Kind:        broker.KindHeaderSet,
			Header:      "x-api-key",
			Secret:      "injected-real-value",
		}},
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	hsR, hsW, err := os.Pipe()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- broker.RunSidecar(ctx, bytes.NewReader(raw), hsW)
		_ = hsW.Close()
	}()

	// The sidecar writes its resolved listen address as one JSON line.
	line, err := bufio.NewReader(hsR).ReadBytes('\n')
	require.NoError(t, err)
	var hs broker.Handshake
	require.NoError(t, json.Unmarshal(line, &hs))
	require.NotEmpty(t, hs.Addr)

	req, err := http.NewRequest(http.MethodPost, "http://"+hs.Addr+"/v1/messages", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dummy")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	assert.Equal(t, "injected-real-value", upstream.gotKey, "sidecar injected the real key")
	assert.Empty(t, upstream.gotAuth, "sidecar stripped the placeholder")

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "RunSidecar returns cleanly on ctx cancel")
	case <-time.After(2 * time.Second):
		t.Fatal("RunSidecar did not return after ctx cancel")
	}
}

func TestRunSidecar_RejectsUnsupportedBindingKind(t *testing.T) {
	cfg := broker.SidecarConfig{
		UpstreamURL: "http://api.example.com",
		BindAddr:    "127.0.0.1:0",
		Bindings: []broker.BindingConfig{{
			Destination: "api.example.com",
			Kind:        "request-signer", // reserved, not brokerable via the sidecar
			Secret:      "k",
		}},
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	err = broker.RunSidecar(context.Background(), bytes.NewReader(raw), io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported binding kind")
}

func TestRunSidecar_RejectsBadConfig(t *testing.T) {
	err := broker.RunSidecar(context.Background(), bytes.NewReader([]byte("not json")), io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read sidecar config")
}
