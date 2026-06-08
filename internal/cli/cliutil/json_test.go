package cliutil_test

// ABOUTME: Unit tests for JSON output helper functions.

import (
	"bytes"
	"errors"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonEnabled_Default(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	assert.False(t, cliutil.JSONEnabled(cmd))
}

func TestJsonEnabled_Set(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	assert.True(t, cliutil.JSONEnabled(cmd))
}

func TestWriteJSON_SimpleObject(t *testing.T) {
	var buf bytes.Buffer
	err := cliutil.WriteJSON(&buf, map[string]string{"key": "value"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"key": "value"}`, buf.String())
	assert.True(t, buf.Bytes()[buf.Len()-1] == '\n', "should end with newline")
}

func TestWriteJSON_Array(t *testing.T) {
	var buf bytes.Buffer
	err := cliutil.WriteJSON(&buf, []string{"a", "b"})
	require.NoError(t, err)
	assert.JSONEq(t, `["a", "b"]`, buf.String())
}

func TestWriteJSON_EmptyArray(t *testing.T) {
	var buf bytes.Buffer
	err := cliutil.WriteJSON(&buf, []string{})
	require.NoError(t, err)
	assert.Equal(t, "[]\n", buf.String())
}

func TestWriteJSON_NilSlice(t *testing.T) {
	var buf bytes.Buffer
	var s []string
	err := cliutil.WriteJSON(&buf, s)
	require.NoError(t, err)
	// nil slice marshals as "null" in Go
	assert.Equal(t, "null\n", buf.String())
}

func TestWriteJSONList_WrapsInEnvelope(t *testing.T) {
	var buf bytes.Buffer
	err := cliutil.WriteJSONList(&buf, "backends", []string{"docker", "tart"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"backends": ["docker", "tart"]}`, buf.String())
}

func TestWriteJSONList_NilSliceIsEmptyArray(t *testing.T) {
	var buf bytes.Buffer
	var items []string
	err := cliutil.WriteJSONList(&buf, "agents", items)
	require.NoError(t, err)
	// A nil slice must serialize as [] inside the envelope, never null.
	assert.JSONEq(t, `{"agents": []}`, buf.String())
}

func TestEmptyIfNil_NilBecomesEmptyNonNil(t *testing.T) {
	var s []int
	got := cliutil.EmptyIfNil(s)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestEmptyIfNil_NonNilUnchanged(t *testing.T) {
	s := []int{1, 2}
	assert.Equal(t, []int{1, 2}, cliutil.EmptyIfNil(s))
}

func TestWriteJSONError(t *testing.T) {
	var buf bytes.Buffer
	cliutil.WriteJSONError(&buf, errors.New("something broke"))
	assert.JSONEq(t, `{"error": "something broke"}`, buf.String())
}

func TestErrJSONNotSupported(t *testing.T) {
	err := cliutil.ErrJSONNotSupported("attach")
	assert.Contains(t, err.Error(), "attach")
	assert.Contains(t, err.Error(), "not supported")
}

func TestEffectiveYes_NoFlags(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	assert.False(t, cliutil.EffectiveYes(cmd))
}

func TestEffectiveYes_YesOnly(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, cmd.Flags().Set("yes", "true"))
	assert.True(t, cliutil.EffectiveYes(cmd))
}

func TestEffectiveYes_JSONOnly(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	assert.True(t, cliutil.EffectiveYes(cmd))
}

func TestEffectiveYes_Both(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	require.NoError(t, cmd.Flags().Set("yes", "true"))
	assert.True(t, cliutil.EffectiveYes(cmd))
}
