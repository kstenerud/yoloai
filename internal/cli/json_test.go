package cli

// ABOUTME: Unit tests for JSON output helper functions.

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonEnabled_Default(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	assert.False(t, jsonEnabled(cmd))
}

func TestJsonEnabled_Set(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	assert.True(t, jsonEnabled(cmd))
}

func TestWriteJSON_SimpleObject(t *testing.T) {
	var buf bytes.Buffer
	err := writeJSON(&buf, map[string]string{"key": "value"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"key": "value"}`, buf.String())
	assert.True(t, buf.Bytes()[buf.Len()-1] == '\n', "should end with newline")
}

func TestWriteJSON_Array(t *testing.T) {
	var buf bytes.Buffer
	err := writeJSON(&buf, []string{"a", "b"})
	require.NoError(t, err)
	assert.JSONEq(t, `["a", "b"]`, buf.String())
}

func TestWriteJSON_EmptyArray(t *testing.T) {
	var buf bytes.Buffer
	err := writeJSON(&buf, []string{})
	require.NoError(t, err)
	assert.Equal(t, "[]\n", buf.String())
}

func TestWriteJSON_NilSlice(t *testing.T) {
	var buf bytes.Buffer
	var s []string
	err := writeJSON(&buf, s)
	require.NoError(t, err)
	// nil slice marshals as "null" in Go
	assert.Equal(t, "null\n", buf.String())
}

func TestWriteJSONError(t *testing.T) {
	var buf bytes.Buffer
	writeJSONError(&buf, errors.New("something broke"))
	assert.JSONEq(t, `{"error": "something broke"}`, buf.String())
}

func TestErrJSONNotSupported(t *testing.T) {
	err := errJSONNotSupported("attach")
	assert.Contains(t, err.Error(), "attach")
	assert.Contains(t, err.Error(), "not supported")
}

func TestRequireYesForJSON_NoJSON(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	assert.NoError(t, requireYesForJSON(cmd))
}

func TestRequireYesForJSON_JSONWithoutYes(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	err := requireYesForJSON(cmd)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")
}

func TestRequireYesForJSON_JSONWithYes(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	require.NoError(t, cmd.Flags().Set("yes", "true"))
	assert.NoError(t, requireYesForJSON(cmd))
}
