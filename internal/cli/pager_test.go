package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunPager_NonTTY(t *testing.T) {
	// In test environment, stdout is not a TTY, so content should be copied directly.
	content := "line1\nline2\nline3\n"
	r := strings.NewReader(content)

	// Capture stdout
	origStdout := os.Stdout
	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = pw

	runErr := RunPager(r)

	require.NoError(t, pw.Close())
	os.Stdout = origStdout

	var buf bytes.Buffer
	_, err = io.Copy(&buf, pr)
	require.NoError(t, err)
	require.NoError(t, pr.Close())

	assert.NoError(t, runErr)
	assert.Equal(t, content, buf.String())
}
