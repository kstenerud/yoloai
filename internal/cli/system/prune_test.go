package system

// ABOUTME: Unit guard for `yoloai system prune` flag wiring: --trash is the
// ABOUTME: explicit opt-in that widens the scope to recoverable trash, and --yes
// ABOUTME: only suppresses prompts (it never authorizes the wider scope).

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPruneCmd_TrashIsExplicitSelector(t *testing.T) {
	cmd := newSystemPruneCmd()
	// --trash selects the wider, recoverable-data scope (parallel to --images).
	assert.NotNil(t, cmd.Flags().Lookup("trash"))
	// --yes survives, but only as a prompt-suppressor; it must not be the thing
	// that authorizes emptying the trash. --images stays a separate selector too.
	assert.NotNil(t, cmd.Flags().Lookup("yes"))
	assert.NotNil(t, cmd.Flags().Lookup("images"))
}
