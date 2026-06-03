// ABOUTME: Tests for SystemClient.Logs — the public activity stream forwards
// ABOUTME: library frames verbatim and reports a missing sandbox as an error.
package yoloai

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func drainEvents(t *testing.T, ch <-chan LogEvent) []LogEvent {
	t.Helper()
	var out []LogEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatal("timed out draining log events")
		}
	}
}

func TestSystemClient_Logs_ForwardsFrames(t *testing.T) {
	dir := t.TempDir()
	sc, err := NewSystemClient(SystemOptions{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(sc.layout.SandboxDir("box"), 0750))
	cliPath := sc.LogPaths("box").CLI
	require.NoError(t, os.MkdirAll(filepath.Dir(cliPath), 0750))
	line := `{"ts":"2026-03-15T10:00:00.000Z","level":"info","event":"hello"}`
	require.NoError(t, os.WriteFile(cliPath, []byte(line+"\n"), 0600))

	ch, err := sc.Logs(context.Background(), "box", LogOptions{})
	require.NoError(t, err)
	events := drainEvents(t, ch)

	require.Len(t, events, 1)
	assert.Equal(t, LogSourceCLI, events[0].Source)
	assert.Equal(t, "info", events[0].Level)
	assert.Equal(t, line, string(events[0].Raw))
}

func TestSystemClient_Logs_MissingSandbox(t *testing.T) {
	dir := t.TempDir()
	sc, err := NewSystemClient(SystemOptions{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)

	_, err = sc.Logs(context.Background(), "ghost", LogOptions{})
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}
