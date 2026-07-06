// ABOUTME: Tests the Linux /proc cmdline matcher that identifies `__inject`
// ABOUTME: injector processes spawned by this binary.
//go:build linux

package broker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func cmdline(args ...string) []byte {
	var b []byte
	for _, a := range args {
		b = append(b, []byte(a)...)
		b = append(b, 0)
	}
	return b
}

func TestInjectorArgvMatches(t *testing.T) {
	// argv[0] basename == selfBase and argv[1] == the inject verb.
	assert.True(t, injectorArgvMatches(cmdline("/home/u/yoloai/yoloai", InjectVerb), "yoloai"))
	assert.True(t, injectorArgvMatches(cmdline("yoloai", InjectVerb, "--extra"), "yoloai"))

	// Wrong binary basename.
	assert.False(t, injectorArgvMatches(cmdline("/usr/bin/other", InjectVerb), "yoloai"))
	// Not the inject verb.
	assert.False(t, injectorArgvMatches(cmdline("yoloai", "system", "prune"), "yoloai"))
	// Only one argv element (no verb).
	assert.False(t, injectorArgvMatches(cmdline("yoloai"), "yoloai"))
	// Empty cmdline.
	assert.False(t, injectorArgvMatches(nil, "yoloai"))
}
