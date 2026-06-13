// ABOUTME: Tests for StreamLogs — backlog merge-sort across sources, level and
// ABOUTME: since filtering, missing-sandbox/invalid-level errors, channel close.
package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

func logStreamLayout(t *testing.T) (config.Layout, string) {
	t.Helper()
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	name := "box"
	createTestSandbox(t, tmp, name, filepath.Join(tmp, "host"), store.DirModeCopy)
	return layout, name
}

// writeJSONL writes content to a source's JSONL file, creating logs/ as needed.
func writeJSONL(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

// drain collects every frame the channel yields until it closes.
func drain(t *testing.T, ch <-chan LogFrame) []LogFrame {
	t.Helper()
	var out []LogFrame
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, f)
		case <-timeout:
			t.Fatal("timed out draining log frames")
		}
	}
}

func TestStreamLogs_MissingSandbox(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	_, err := StreamLogs(context.Background(), layout, "ghost", LogStreamOptions{})
	require.ErrorIs(t, err, store.ErrSandboxNotFound,
		"a missing sandbox must surface ErrSandboxNotFound, not a generic error")
}

func TestStreamLogs_InvalidLevel(t *testing.T) {
	layout, name := logStreamLayout(t)
	_, err := StreamLogs(context.Background(), layout, name, LogStreamOptions{MinLevel: "loud"})
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "an unknown level must be a *UsageError owned by the library")
	assert.Contains(t, ue.Error(), "loud", "the error should name the rejected level")
}

func TestStreamLogs_BacklogMergeSorted(t *testing.T) {
	layout, name := logStreamLayout(t)
	dir := layout.SandboxDir(name)

	writeJSONL(t, store.CLIJSONLPath(dir),
		`{"ts":"2026-03-15T10:00:02.000Z","level":"info","event":"c2"}`+"\n"+
			`{"ts":"2026-03-15T10:00:00.000Z","level":"info","event":"c0"}`+"\n")
	writeJSONL(t, store.SandboxJSONLPath(dir),
		`{"ts":"2026-03-15T10:00:01.000Z","level":"info","event":"s1"}`+"\n")

	ch, err := StreamLogs(context.Background(), layout, name, LogStreamOptions{})
	require.NoError(t, err)
	frames := drain(t, ch)

	require.Len(t, frames, 3)
	assert.Equal(t, store.LogSourceCLI, frames[0].Source)
	assert.Equal(t, store.LogSourceSandbox, frames[1].Source)
	assert.Equal(t, store.LogSourceCLI, frames[2].Source)
	// Raw is verbatim (carries the event we wrote).
	assert.Contains(t, string(frames[0].Raw), `"event":"c0"`)
	assert.Contains(t, string(frames[1].Raw), `"event":"s1"`)
	assert.Contains(t, string(frames[2].Raw), `"event":"c2"`)
}

func TestStreamLogs_LevelFilter(t *testing.T) {
	layout, name := logStreamLayout(t)
	writeJSONL(t, store.CLIJSONLPath(layout.SandboxDir(name)),
		`{"ts":"2026-03-15T10:00:00.000Z","level":"debug","event":"d"}`+"\n"+
			`{"ts":"2026-03-15T10:00:01.000Z","level":"warn","event":"w"}`+"\n")

	ch, err := StreamLogs(context.Background(), layout, name, LogStreamOptions{MinLevel: "warn"})
	require.NoError(t, err)
	frames := drain(t, ch)

	require.Len(t, frames, 1)
	assert.Equal(t, "warn", frames[0].Level)
}

func TestStreamLogs_SinceFilter(t *testing.T) {
	layout, name := logStreamLayout(t)
	writeJSONL(t, store.CLIJSONLPath(layout.SandboxDir(name)),
		`{"ts":"2026-03-15T10:00:00.000Z","level":"info","event":"old"}`+"\n"+
			`{"ts":"2026-03-15T10:00:05.000Z","level":"info","event":"new"}`+"\n")

	cutoff, _ := time.Parse(time.RFC3339, "2026-03-15T10:00:03Z")
	ch, err := StreamLogs(context.Background(), layout, name, LogStreamOptions{Since: cutoff})
	require.NoError(t, err)
	frames := drain(t, ch)

	require.Len(t, frames, 1)
	assert.Contains(t, string(frames[0].Raw), `"event":"new"`)
}

func TestStreamLogs_SourceFilter(t *testing.T) {
	layout, name := logStreamLayout(t)
	dir := layout.SandboxDir(name)
	writeJSONL(t, store.CLIJSONLPath(dir), `{"ts":"2026-03-15T10:00:00.000Z","level":"info","event":"c"}`+"\n")
	writeJSONL(t, store.SandboxJSONLPath(dir), `{"ts":"2026-03-15T10:00:01.000Z","level":"info","event":"s"}`+"\n")

	ch, err := StreamLogs(context.Background(), layout, name, LogStreamOptions{
		Sources: []store.LogSource{store.LogSourceSandbox},
	})
	require.NoError(t, err)
	frames := drain(t, ch)

	require.Len(t, frames, 1)
	assert.Equal(t, store.LogSourceSandbox, frames[0].Source)
}

func TestStreamLogs_EmptyBacklogClosesChannel(t *testing.T) {
	layout, name := logStreamLayout(t)
	ch, err := StreamLogs(context.Background(), layout, name, LogStreamOptions{})
	require.NoError(t, err)
	frames := drain(t, ch)
	assert.Empty(t, frames)
}

func TestStreamLogs_FollowEmitsBacklogThenClosesOnCancel(t *testing.T) {
	layout, name := logStreamLayout(t)
	writeJSONL(t, store.CLIJSONLPath(layout.SandboxDir(name)),
		`{"ts":"2026-03-15T10:00:00.000Z","level":"info","event":"backlog"}`+"\n")

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := StreamLogs(ctx, layout, name, LogStreamOptions{Follow: true})
	require.NoError(t, err)

	// Backlog frame arrives first.
	select {
	case f, ok := <-ch:
		require.True(t, ok)
		assert.Contains(t, string(f.Raw), `"event":"backlog"`)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for backlog frame")
	}

	// Cancelling the context must drain the tailers and close the channel.
	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow stream did not close after ctx cancel")
	}
}

func TestStreamLogs_InvalidLinesSkipped(t *testing.T) {
	layout, name := logStreamLayout(t)
	writeJSONL(t, store.CLIJSONLPath(layout.SandboxDir(name)),
		"not json\n"+
			`{"ts":"2026-03-15T10:00:00.000Z","level":"info","event":"ok"}`+"\n"+
			"\n")

	ch, err := StreamLogs(context.Background(), layout, name, LogStreamOptions{})
	require.NoError(t, err)
	frames := drain(t, ch)
	require.Len(t, frames, 1)
	assert.Contains(t, string(frames[0].Raw), `"event":"ok"`)
}
