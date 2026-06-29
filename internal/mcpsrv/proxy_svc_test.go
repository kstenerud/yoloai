// ABOUTME: Tests for ProxyServer methods that use SandboxService — handleProxyDiff,
// ABOUTME: tryHandleLocalToolCall (sandbox_diff branch), ensureRunning, createSandbox.
package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── handleProxyDiff ───────────────────────────────────────────────────────────

func TestHandleProxyDiff_Empty(t *testing.T) {
	svc := &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			assert.Equal(t, "mybox", name)
			assert.False(t, opts.Stat)
			return "", nil
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	result := p.handleProxyDiff(context.Background(), map[string]any{})
	content := result["content"].([]map[string]any)
	assert.Equal(t, "No changes to diff", content[0]["text"])
}

func TestHandleProxyDiff_NonEmpty(t *testing.T) {
	const fakeDiff = "--- a\n+++ b\n@@ -1 +1 @@\n"
	svc := &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return fakeDiff, nil
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	result := p.handleProxyDiff(context.Background(), map[string]any{"stat": true})
	content := result["content"].([]map[string]any)
	assert.Equal(t, fakeDiff, content[0]["text"])
}

func TestHandleProxyDiff_Error(t *testing.T) {
	svc := &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return "", fmt.Errorf("connection refused")
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	result := p.handleProxyDiff(context.Background(), map[string]any{})
	content := result["content"].([]map[string]any)
	text := content[0]["text"].(string)
	assert.Contains(t, text, "[ERROR]")
	assert.Contains(t, text, "connection refused")
}

// ── tryHandleLocalToolCall — sandbox_diff handled ─────────────────────────────

func TestTryHandleLocalToolCall_SandboxDiff_Handled(t *testing.T) {
	svc := &fakeService{
		DiffFn: func(_ context.Context, name string, opts yoloai.WorkdirDiffOptions) (string, error) {
			return "the diff", nil
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	msg := jsonRPCMsg{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      json.RawMessage(`42`),
		Params:  json.RawMessage(`{"name":"sandbox_diff","arguments":{}}`),
	}
	localIDs := map[string]bool{}
	var writtenResponse jsonRPCMsg

	handled, err := p.tryHandleLocalToolCall(context.Background(), msg, &sync.Mutex{}, localIDs, func(m jsonRPCMsg) error {
		writtenResponse = m
		return nil
	})

	require.NoError(t, err)
	assert.True(t, handled)
	assert.Equal(t, json.RawMessage(`42`), writtenResponse.ID)
	assert.True(t, localIDs["42"], "response ID must be tracked as local")
}

// ── ensureRunning ─────────────────────────────────────────────────────────────

func TestEnsureRunning_SandboxRunning_ReturnsEnvironment(t *testing.T) {
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusActive,
			}, nil
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	env, err := p.ensureRunning(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mybox", env.Name)
}

func TestEnsureRunning_SandboxStopped_StartsIt(t *testing.T) {
	var startCalled bool
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusStopped,
			}, nil
		},
		StartFn: func(_ context.Context, name string) error {
			startCalled = true
			return nil
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	env, err := p.ensureRunning(context.Background())
	require.NoError(t, err)
	assert.True(t, startCalled, "Start must be called for a stopped sandbox")
	assert.Equal(t, "mybox", env.Name)
}

func TestEnsureRunning_SandboxNotFound_Creates(t *testing.T) {
	var inspectCount int
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			inspectCount++
			if inspectCount == 1 {
				return nil, yoloai.ErrSandboxNotFound
			}
			// Second call (after createSandbox → p.svc.Inspect)
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusIdle,
			}, nil
		},
		CreateSandboxFn: func(_ context.Context, opts yoloai.SandboxCreateOptions) error {
			return nil
		},
		StartFn: func(_ context.Context, name string) error {
			return nil
		},
	}
	p := &ProxyServer{
		svc:         svc,
		sandboxName: "mybox",
		opts:        ProxyOptions{Workdir: yoloai.DirSpec{Path: "/tmp/work"}},
	}
	env, err := p.ensureRunning(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "mybox", env.Name)
	assert.Equal(t, 2, inspectCount, "Inspect must be called twice: once to check, once after create")
}

func TestEnsureRunning_InspectError(t *testing.T) {
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return nil, fmt.Errorf("backend unreachable")
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	_, err := p.ensureRunning(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspect sandbox")
}

func TestEnsureRunning_SandboxStopped_StartError(t *testing.T) {
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusStopped,
			}, nil
		},
		StartFn: func(_ context.Context, name string) error {
			return fmt.Errorf("start refused")
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	_, err := p.ensureRunning(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start sandbox")
}

func TestEnsureRunning_UnexpectedStatus(t *testing.T) {
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusBroken,
			}, nil
		},
	}
	p := &ProxyServer{svc: svc, sandboxName: "mybox"}
	_, err := p.ensureRunning(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected state")
}

// ── createSandbox ─────────────────────────────────────────────────────────────

func TestCreateSandbox_NoWorkdir_Error(t *testing.T) {
	p := &ProxyServer{
		svc:         &fakeService{},
		sandboxName: "mybox",
		opts:        ProxyOptions{}, // empty Workdir
	}
	_, err := p.createSandbox(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provide --workdir")
}

func TestCreateSandbox_CreateError(t *testing.T) {
	svc := &fakeService{
		CreateSandboxFn: func(_ context.Context, opts yoloai.SandboxCreateOptions) error {
			return fmt.Errorf("image pull failed")
		},
	}
	p := &ProxyServer{
		svc:         svc,
		sandboxName: "mybox",
		opts:        ProxyOptions{Workdir: yoloai.DirSpec{Path: "/tmp/work"}},
	}
	_, err := p.createSandbox(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create sandbox")
}

func TestCreateSandbox_StartError(t *testing.T) {
	svc := &fakeService{
		StartFn: func(_ context.Context, name string) error {
			return fmt.Errorf("port conflict")
		},
	}
	p := &ProxyServer{
		svc:         svc,
		sandboxName: "mybox",
		opts:        ProxyOptions{Workdir: yoloai.DirSpec{Path: "/tmp/work"}},
	}
	_, err := p.createSandbox(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start sandbox")
}

func TestCreateSandbox_InspectError(t *testing.T) {
	var inspectCount int
	svc := &fakeService{
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			inspectCount++
			return nil, fmt.Errorf("inspect failed after create")
		},
	}
	p := &ProxyServer{
		svc:         svc,
		sandboxName: "mybox",
		opts:        ProxyOptions{Workdir: yoloai.DirSpec{Path: "/tmp/work"}},
	}
	_, err := p.createSandbox(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspect sandbox")
}

func TestCreateSandbox_Success(t *testing.T) {
	var createCalled, startCalled bool
	svc := &fakeService{
		CreateSandboxFn: func(_ context.Context, opts yoloai.SandboxCreateOptions) error {
			createCalled = true
			assert.Equal(t, "mybox", opts.Name)
			return nil
		},
		StartFn: func(_ context.Context, name string) error {
			startCalled = true
			return nil
		},
		InspectFn: func(_ context.Context, name string) (*yoloai.SandboxInfo, error) {
			return &yoloai.SandboxInfo{
				Environment: &yoloai.Environment{Name: name},
				Status:      yoloai.StatusActive,
			}, nil
		},
	}
	p := &ProxyServer{
		svc:         svc,
		sandboxName: "mybox",
		opts:        ProxyOptions{Workdir: yoloai.DirSpec{Path: "/tmp/work"}},
	}
	env, err := p.createSandbox(context.Background())
	require.NoError(t, err)
	assert.True(t, createCalled)
	assert.True(t, startCalled)
	assert.Equal(t, "mybox", env.Name)
}
