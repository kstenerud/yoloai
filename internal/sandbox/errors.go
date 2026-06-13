// ABOUTME: Sentinel errors for sandbox operations.
package sandbox

import (
	"errors"

	"github.com/kstenerud/yoloai/internal/sandbox/create"
	"github.com/kstenerud/yoloai/internal/store"
)

// Sentinel errors for sandbox operations.
var (
	// ErrSandboxNotFound is forwarded from store. See store.ErrSandboxNotFound.
	ErrSandboxNotFound = store.ErrSandboxNotFound

	// ErrSandboxExists is the canonical sentinel for "sandbox already exists";
	// its definition lives in the create leaf so the create pipeline can produce
	// it without importing this façade. This alias preserves the public
	// sandbox.ErrSandboxExists symbol and the yoloai.ErrSandboxExists re-export.
	ErrSandboxExists = create.ErrSandboxExists

	// ErrMissingAPIKey is the canonical sentinel for "required API key not set";
	// its definition lives in the create leaf. This alias preserves the public
	// sandbox.ErrMissingAPIKey symbol.
	ErrMissingAPIKey       = create.ErrMissingAPIKey
	ErrContainerNotRunning = errors.New("container is not running")
)
