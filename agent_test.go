// ABOUTME: Tests for Agent.Logs — the public activity stream forwards library
// ABOUTME: frames verbatim and reports a missing sandbox as an error.
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

func TestAgentLogs_ForwardsFrames(t *testing.T) {
	dir := t.TempDir()
	c, err := NewWithOptions(context.Background(), Options{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	require.NoError(t, os.MkdirAll(c.layout.SandboxDir("box"), 0750))
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	cliPath := sb.LogPaths().CLI
	require.NoError(t, os.MkdirAll(filepath.Dir(cliPath), 0750))
	line := `{"ts":"2026-03-15T10:00:00.000Z","level":"info","event":"hello"}`
	require.NoError(t, os.WriteFile(cliPath, []byte(line+"\n"), 0600))

	ch, err := sb.Agent().Logs(context.Background(), LogOptions{})
	require.NoError(t, err)
	events := drainEvents(t, ch)

	require.Len(t, events, 1)
	assert.Equal(t, LogSourceCLI, events[0].Source)
	assert.Equal(t, "info", events[0].Level)
	assert.Equal(t, line, string(events[0].Raw))
}

func TestAgentLogs_MissingSandbox(t *testing.T) {
	dir := t.TempDir()
	c, err := NewWithOptions(context.Background(), Options{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	_, err = c.Sandbox("ghost")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}
