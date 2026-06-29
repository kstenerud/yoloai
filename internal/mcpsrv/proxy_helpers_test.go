// ABOUTME: Unit tests for the MCP proxy's pure/io helper functions — JSON-RPC
// ABOUTME: message routing, tool injection, and serialization with no live client.

package mcpsrv

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── mustMarshal ───────────────────────────────────────────────────────────────

func TestMustMarshal_ValidValue(t *testing.T) {
	got := mustMarshal(map[string]any{"hello": "world"})
	assert.JSONEq(t, `{"hello":"world"}`, string(got))
}

func TestMustMarshal_Unmarshalable_ReturnsNull(t *testing.T) {
	got := mustMarshal(make(chan int)) // channels cannot be JSON-marshaled
	assert.Equal(t, json.RawMessage(`null`), got)
}

// ── mcpTextContent ────────────────────────────────────────────────────────────

func TestMcpTextContent_Shape(t *testing.T) {
	result := mcpTextContent("hello world")
	content, ok := result["content"].([]map[string]any)
	require.True(t, ok, "expected []map[string]any under 'content'")
	require.Len(t, content, 1)
	assert.Equal(t, "text", content[0]["type"])
	assert.Equal(t, "hello world", content[0]["text"])
}

// ── discardLocalResponse ──────────────────────────────────────────────────────

func TestDiscardLocalResponse_NilID_NotDiscarded(t *testing.T) {
	msg := &jsonRPCMsg{ID: nil}
	localIDs := map[string]bool{}
	assert.False(t, discardLocalResponse(msg, &sync.Mutex{}, localIDs))
}

func TestDiscardLocalResponse_IDNotInMap_NotDiscarded(t *testing.T) {
	msg := &jsonRPCMsg{ID: json.RawMessage(`42`)}
	localIDs := map[string]bool{"1": true}
	assert.False(t, discardLocalResponse(msg, &sync.Mutex{}, localIDs))
	assert.Contains(t, localIDs, "1", "unrelated IDs must not be removed")
}

func TestDiscardLocalResponse_IDInMap_DiscardedAndRemoved(t *testing.T) {
	msg := &jsonRPCMsg{ID: json.RawMessage(`42`)}
	localIDs := map[string]bool{"42": true, "99": true}
	assert.True(t, discardLocalResponse(msg, &sync.Mutex{}, localIDs))
	assert.NotContains(t, localIDs, "42", "matched ID must be removed")
	assert.Contains(t, localIDs, "99", "other IDs must be left intact")
}

// ── injectToolsIfNeeded ───────────────────────────────────────────────────────

func TestInjectToolsIfNeeded_NilResult_NoChange(t *testing.T) {
	msg := &jsonRPCMsg{ID: json.RawMessage(`1`), Result: nil}
	injectToolsIfNeeded(msg)
	assert.Nil(t, msg.Result)
}

func TestInjectToolsIfNeeded_NilID_NoChange(t *testing.T) {
	msg := &jsonRPCMsg{ID: nil, Result: json.RawMessage(`{"tools":[]}`)}
	before := string(msg.Result)
	injectToolsIfNeeded(msg)
	assert.Equal(t, before, string(msg.Result))
}

func TestInjectToolsIfNeeded_NoToolsKey_NoChange(t *testing.T) {
	msg := &jsonRPCMsg{
		ID:     json.RawMessage(`1`),
		Result: json.RawMessage(`{"resources":[]}`),
	}
	injectToolsIfNeeded(msg)
	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(msg.Result, &result))
	_, hasTools := result["tools"]
	assert.False(t, hasTools, "tools key must not be injected when the result has no tools field")
}

func TestInjectToolsIfNeeded_EmptyTools_InjectsSandboxDiff(t *testing.T) {
	msg := &jsonRPCMsg{
		ID:     json.RawMessage(`1`),
		Result: json.RawMessage(`{"tools":[]}`),
	}
	injectToolsIfNeeded(msg)

	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(msg.Result, &result))
	var tools []map[string]any
	require.NoError(t, json.Unmarshal(result["tools"], &tools))
	require.Len(t, tools, 1)
	assert.Equal(t, "sandbox_diff", tools[0]["name"])
}

func TestInjectToolsIfNeeded_PreservesExistingTools(t *testing.T) {
	msg := &jsonRPCMsg{
		ID:     json.RawMessage(`1`),
		Result: json.RawMessage(`{"tools":[{"name":"existing_tool"}]}`),
	}
	injectToolsIfNeeded(msg)

	var result map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(msg.Result, &result))
	var tools []map[string]any
	require.NoError(t, json.Unmarshal(result["tools"], &tools))
	require.Len(t, tools, 2, "existing tool and sandbox_diff must both be present")
	names := []string{tools[0]["name"].(string), tools[1]["name"].(string)}
	assert.Contains(t, names, "existing_tool")
	assert.Contains(t, names, "sandbox_diff")
}

// ── handleOuterMessage ────────────────────────────────────────────────────────

func TestHandleOuterMessage_InvalidJSON_ForwardsRawLine(t *testing.T) {
	p := &ProxyServer{}
	var inner bytes.Buffer
	line := []byte("not valid json here")

	err := p.handleOuterMessage(line, &inner, &sync.Mutex{}, map[string]bool{}, func(jsonRPCMsg) error { return nil })

	assert.NoError(t, err)
	assert.Contains(t, inner.String(), "not valid json here")
}

func TestHandleOuterMessage_NonToolsCallForwarded(t *testing.T) {
	p := &ProxyServer{}
	var inner bytes.Buffer
	line := []byte(`{"jsonrpc":"2.0","method":"initialize","params":{},"id":1}`)

	err := p.handleOuterMessage(line, &inner, &sync.Mutex{}, map[string]bool{}, func(jsonRPCMsg) error { return nil })

	assert.NoError(t, err)
	assert.Contains(t, inner.String(), "initialize")
}

func TestHandleOuterMessage_NonSandboxDiffToolCall_Forwarded(t *testing.T) {
	// nil client: sandbox_diff must NOT be called (would panic); other tool calls
	// must be forwarded to the inner server, not handled locally.
	p := &ProxyServer{client: nil}
	var inner bytes.Buffer
	line := []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"other_tool","arguments":{}},"id":2}`)

	err := p.handleOuterMessage(line, &inner, &sync.Mutex{}, map[string]bool{}, func(jsonRPCMsg) error { return nil })

	assert.NoError(t, err)
	assert.Contains(t, inner.String(), "other_tool")
}

// ── tryHandleLocalToolCall ────────────────────────────────────────────────────

func TestTryHandleLocalToolCall_NonSandboxDiff_NotHandled(t *testing.T) {
	// Non-sandbox_diff tool calls must be forwarded (not consumed locally),
	// and writeOut must not be called.
	p := &ProxyServer{client: nil}
	msg := jsonRPCMsg{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      json.RawMessage(`1`),
		Params:  json.RawMessage(`{"name":"other_tool","arguments":{}}`),
	}
	var writeOutCalled bool
	handled, err := p.tryHandleLocalToolCall(msg, &sync.Mutex{}, map[string]bool{}, func(jsonRPCMsg) error {
		writeOutCalled = true
		return nil
	})
	assert.False(t, handled)
	assert.NoError(t, err)
	assert.False(t, writeOutCalled)
}

func TestTryHandleLocalToolCall_InvalidParams_NotHandled(t *testing.T) {
	// Unparseable params → treated as non-matching and forwarded to inner server.
	p := &ProxyServer{client: nil}
	msg := jsonRPCMsg{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      json.RawMessage(`1`),
		Params:  json.RawMessage(`{not valid json`),
	}
	handled, err := p.tryHandleLocalToolCall(msg, &sync.Mutex{}, map[string]bool{}, func(jsonRPCMsg) error { return nil })
	assert.False(t, handled)
	assert.NoError(t, err)
}
