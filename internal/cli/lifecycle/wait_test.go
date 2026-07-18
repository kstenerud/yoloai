// ABOUTME: Tests for the wait command's flag parsing and result rendering. The
// ABOUTME: wait loop itself (pollUntil) is unit-tested in the library package.

package lifecycle

import (
	"bytes"
	"encoding/json"
	"testing"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWaitCondition(t *testing.T) {
	idle, err := parseWaitCondition("idle")
	require.NoError(t, err)
	assert.Equal(t, yoloai.WaitForIdle, idle)

	exit, err := parseWaitCondition("exit")
	require.NoError(t, err)
	assert.Equal(t, yoloai.WaitForExit, exit)

	_, err = parseWaitCondition("done")
	assert.Error(t, err, "an unknown condition is a usage error")

	_, err = parseWaitCondition("")
	assert.Error(t, err, "empty condition is rejected (the flag default supplies 'idle')")
}

func TestWriteWaitResult_Human(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	info := &yoloai.SandboxInfo{Environment: &yoloai.Environment{Name: "mybox"}, Status: yoloai.StatusIdle}
	require.NoError(t, writeWaitResult(cmd, info))

	assert.Contains(t, buf.String(), "mybox")
	assert.Contains(t, buf.String(), string(yoloai.StatusIdle))
}

func TestWriteWaitResult_JSON(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	info := &yoloai.SandboxInfo{Environment: &yoloai.Environment{Name: "mybox"}, Status: yoloai.StatusDone}
	require.NoError(t, writeWaitResult(cmd, info))

	assert.True(t, json.Valid(buf.Bytes()), "JSON mode emits valid JSON")
	assert.Contains(t, buf.String(), `"status": "`+string(yoloai.StatusDone)+`"`)
	assert.Contains(t, buf.String(), "mybox")
}

func TestWaitExitCode(t *testing.T) {
	tests := []struct {
		name      string
		status    yoloai.Status
		agentExit *int
		want      int
	}{
		{"done", yoloai.StatusDone, nil, 0},
		{"idle", yoloai.StatusIdle, nil, 0},
		{"stopped", yoloai.StatusStopped, nil, 0},
		{"failed with exit code", yoloai.StatusFailed, new(3), 3},
		{"failed with zero exit code still means failure", yoloai.StatusFailed, new(0), 1},
		{"failed with no recorded exit code", yoloai.StatusFailed, nil, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, waitExitCode(tc.status, tc.agentExit))
		})
	}
}
