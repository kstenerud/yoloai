package sandbox

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfirm_Yes(t *testing.T) {
	var out bytes.Buffer
	confirmed, err := Confirm(context.Background(), "Continue? [y/N] ", strings.NewReader("y\n"), &out)
	require.NoError(t, err)
	assert.True(t, confirmed)
	assert.Equal(t, "Continue? [y/N] ", out.String())
}

func TestConfirm_No(t *testing.T) {
	var out bytes.Buffer
	confirmed, err := Confirm(context.Background(), "Continue? [y/N] ", strings.NewReader("n\n"), &out)
	require.NoError(t, err)
	assert.False(t, confirmed)
}

func TestConfirm_Empty(t *testing.T) {
	var out bytes.Buffer
	confirmed, err := Confirm(context.Background(), "Continue? [y/N] ", strings.NewReader("\n"), &out)
	require.NoError(t, err)
	assert.False(t, confirmed)
}

func TestConfirm_EOF(t *testing.T) {
	var out bytes.Buffer
	confirmed, err := Confirm(context.Background(), "Continue? [y/N] ", strings.NewReader(""), &out)
	require.NoError(t, err)
	assert.False(t, confirmed)
}

func TestConfirm_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var out bytes.Buffer
	confirmed, err := Confirm(ctx, "Continue? [y/N] ", strings.NewReader("y\n"), &out)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, confirmed)
}

func TestReadLine_Normal(t *testing.T) {
	line, err := readLine(context.Background(), strings.NewReader("hello\n"))
	require.NoError(t, err)
	assert.Equal(t, "hello", line)
}

func TestReadLine_EOF(t *testing.T) {
	line, err := readLine(context.Background(), strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, "", line)
}

func TestReadLine_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := readLine(ctx, strings.NewReader("hello\n"))
	assert.ErrorIs(t, err, context.Canceled)
}
