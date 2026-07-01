// ABOUTME: Tests for server.go and tools.go boilerplate coverage — registerTools
// ABOUTME: and createAndStart error branches not covered by handler-level tests.
package mcpsrv

import (
	"context"
	"fmt"
	"testing"

	"github.com/kstenerud/yoloai"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
)

// testCreateOpts builds a minimal SandboxCreateOptions for createAndStart tests.
func testCreateOpts(name string) yoloai.SandboxCreateOptions {
	return yoloai.SandboxCreateOptions{
		Name:    name,
		Workdir: yoloai.DirSpec{Path: "/tmp/work"},
	}
}

// TestRegisterTools_NoPanic verifies that registerTools completes without
// panicking and covers the function's body (all AddTool calls).
func TestRegisterTools_NoPanic(t *testing.T) {
	s := &Server{
		svc: &fakeService{},
		srv: server.NewMCPServer("yoloai", "1.0.0",
			server.WithToolCapabilities(true),
		),
	}
	// Must not panic — all tool definitions and handler wires are exercised.
	assert.NotPanics(t, s.registerTools)
}

// TestCreateAndStart_EnsureSetupError surfaces EnsureSetup failures.
func TestCreateAndStart_EnsureSetupError(t *testing.T) {
	s := &Server{svc: &fakeService{
		EnsureSetupFn: func(_ context.Context) error {
			return fmt.Errorf("setup failed")
		},
	}}
	result := s.createAndStart(context.Background(), testCreateOpts("mybox"))
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "setup")
}

// TestCreateAndStart_CreateSandboxError surfaces CreateSandbox failures.
func TestCreateAndStart_CreateSandboxError(t *testing.T) {
	s := &Server{svc: &fakeService{
		CreateSandboxFn: func(_ context.Context, _ yoloai.SandboxCreateOptions) error {
			return fmt.Errorf("create failed")
		},
	}}
	result := s.createAndStart(context.Background(), testCreateOpts("mybox"))
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "create sandbox")
}

// TestCreateAndStart_StartError surfaces Start failures.
func TestCreateAndStart_StartError(t *testing.T) {
	s := &Server{svc: &fakeService{
		StartFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("start failed")
		},
	}}
	result := s.createAndStart(context.Background(), testCreateOpts("mybox"))
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "start sandbox")
}
