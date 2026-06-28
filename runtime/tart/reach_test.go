package tart

// ABOUTME: Locks tart's documented InjectorReach behavior: unsupported, because the
// ABOUTME: per-VM vmnet bridge isn't host-bindable before the VM is created.

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectorReachUnsupported(t *testing.T) {
	reach, err := (&Runtime{}).InjectorReach(context.Background())
	require.ErrorIs(t, err, runtime.ErrInjectorUnsupported,
		"tart can't bind its per-VM vmnet gateway pre-create; brokering must degrade to direct delivery")
	assert.Empty(t, reach.BindHost)
	assert.Empty(t, reach.DialHost)
}
