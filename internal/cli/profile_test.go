package cli

import (
	"bytes"
	"testing"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
)

// --- printScalarDiff ---

func TestPrintScalarDiff_Equal(t *testing.T) {
	var buf bytes.Buffer
	printed := printScalarDiff(&buf, "Agent", "claude", "claude")
	assert.False(t, printed)
	assert.Empty(t, buf.String())
}

func TestPrintScalarDiff_Addition(t *testing.T) {
	var buf bytes.Buffer
	printed := printScalarDiff(&buf, "Agent", "", "claude")
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "+ Agent:")
	assert.Contains(t, buf.String(), "claude")
}

func TestPrintScalarDiff_Change(t *testing.T) {
	var buf bytes.Buffer
	printed := printScalarDiff(&buf, "Agent", "gemini", "claude")
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "~ Agent:")
	assert.Contains(t, buf.String(), "gemini")
	assert.Contains(t, buf.String(), "claude")
}

// --- printMapDiff ---

func TestPrintMapDiff_EmptyNew(t *testing.T) {
	var buf bytes.Buffer
	printed := printMapDiff(&buf, "Env", map[string]string{"A": "1"}, nil)
	assert.False(t, printed)
	assert.Empty(t, buf.String())
}

func TestPrintMapDiff_NewKey(t *testing.T) {
	var buf bytes.Buffer
	old := map[string]string{}
	new := map[string]string{"KEY": "val"}
	printed := printMapDiff(&buf, "Env", old, new)
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "Env:")
	assert.Contains(t, buf.String(), "+ KEY:")
	assert.Contains(t, buf.String(), "val")
}

func TestPrintMapDiff_ChangedKey(t *testing.T) {
	var buf bytes.Buffer
	old := map[string]string{"KEY": "old"}
	new := map[string]string{"KEY": "new"}
	printed := printMapDiff(&buf, "Env", old, new)
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "~ KEY:")
	assert.Contains(t, buf.String(), "old")
	assert.Contains(t, buf.String(), "new")
}

func TestPrintMapDiff_NoChange(t *testing.T) {
	var buf bytes.Buffer
	old := map[string]string{"KEY": "same"}
	new := map[string]string{"KEY": "same"}
	printed := printMapDiff(&buf, "Env", old, new)
	assert.False(t, printed)
	assert.Empty(t, buf.String())
}

// --- printListAdditions ---

func TestPrintListAdditions_NoAdditions(t *testing.T) {
	var buf bytes.Buffer
	printed := printListAdditions(&buf, "Ports", []string{"8080"}, []string{"8080"})
	assert.False(t, printed)
}

func TestPrintListAdditions_NewItems(t *testing.T) {
	var buf bytes.Buffer
	printed := printListAdditions(&buf, "Ports", []string{"8080"}, []string{"8080", "9090"})
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "Ports:")
	assert.Contains(t, buf.String(), "+ 9090")
}

func TestPrintListAdditions_ShorterNew(t *testing.T) {
	var buf bytes.Buffer
	printed := printListAdditions(&buf, "Ports", []string{"8080", "9090"}, []string{"8080"})
	assert.False(t, printed)
}

// --- printWorkdirDiff ---

func TestPrintWorkdirDiff_NilNew(t *testing.T) {
	var buf bytes.Buffer
	printed := printWorkdirDiff(&buf, &sandbox.ProfileWorkdir{Path: "/a"}, nil)
	assert.False(t, printed)
}

func TestPrintWorkdirDiff_NilOld(t *testing.T) {
	var buf bytes.Buffer
	printed := printWorkdirDiff(&buf, nil, &sandbox.ProfileWorkdir{Path: "/a"})
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "+ Workdir:")
	assert.Contains(t, buf.String(), "/a")
}

func TestPrintWorkdirDiff_Changed(t *testing.T) {
	var buf bytes.Buffer
	old := &sandbox.ProfileWorkdir{Path: "/a"}
	new := &sandbox.ProfileWorkdir{Path: "/b"}
	printed := printWorkdirDiff(&buf, old, new)
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "~ Workdir:")
}

func TestPrintWorkdirDiff_Same(t *testing.T) {
	var buf bytes.Buffer
	w := &sandbox.ProfileWorkdir{Path: "/a", Mode: "copy"}
	printed := printWorkdirDiff(&buf, w, w)
	assert.False(t, printed)
}

// --- formatWorkdir ---

func TestFormatWorkdir_PathOnly(t *testing.T) {
	w := &sandbox.ProfileWorkdir{Path: "/home/user/project"}
	assert.Equal(t, "/home/user/project", formatWorkdir(w))
}

func TestFormatWorkdir_PathAndMode(t *testing.T) {
	w := &sandbox.ProfileWorkdir{Path: "/home/user/project", Mode: "copy"}
	assert.Equal(t, "/home/user/project (copy)", formatWorkdir(w))
}

func TestFormatWorkdir_PathModeAndMount(t *testing.T) {
	w := &sandbox.ProfileWorkdir{Path: "/home/user/project", Mode: "copy", Mount: "/app"}
	assert.Equal(t, "/home/user/project (copy) → /app", formatWorkdir(w))
}

// --- printResourcesDiff ---

func TestPrintResourcesDiff_NilNew(t *testing.T) {
	var buf bytes.Buffer
	printed := printResourcesDiff(&buf, &sandbox.ResourceLimits{CPUs: "2"}, nil)
	assert.False(t, printed)
}

func TestPrintResourcesDiff_NilOld(t *testing.T) {
	var buf bytes.Buffer
	printed := printResourcesDiff(&buf, nil, &sandbox.ResourceLimits{CPUs: "4"})
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "Resources:")
	assert.Contains(t, buf.String(), "+ cpus:")
	assert.Contains(t, buf.String(), "4")
}

func TestPrintResourcesDiff_Changed(t *testing.T) {
	var buf bytes.Buffer
	old := &sandbox.ResourceLimits{CPUs: "2"}
	new := &sandbox.ResourceLimits{CPUs: "4"}
	printed := printResourcesDiff(&buf, old, new)
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "~ cpus:")
	assert.Contains(t, buf.String(), "2")
	assert.Contains(t, buf.String(), "4")
}

func TestPrintResourcesDiff_NoChange(t *testing.T) {
	var buf bytes.Buffer
	r := &sandbox.ResourceLimits{CPUs: "2", Memory: "4g"}
	printed := printResourcesDiff(&buf, r, r)
	assert.False(t, printed)
}

// --- printNetworkDiff ---

func TestPrintNetworkDiff_NilNew(t *testing.T) {
	var buf bytes.Buffer
	printed := printNetworkDiff(&buf, &sandbox.NetworkConfig{Isolated: true}, nil)
	assert.False(t, printed)
}

func TestPrintNetworkDiff_IsolatedChanged(t *testing.T) {
	var buf bytes.Buffer
	old := &sandbox.NetworkConfig{Isolated: false}
	new := &sandbox.NetworkConfig{Isolated: true}
	printed := printNetworkDiff(&buf, old, new)
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "~ Isolated:")
}

func TestPrintNetworkDiff_AllowAdded(t *testing.T) {
	var buf bytes.Buffer
	old := &sandbox.NetworkConfig{Isolated: true, Allow: []string{"a.com"}}
	new := &sandbox.NetworkConfig{Isolated: true, Allow: []string{"a.com", "b.com"}}
	printed := printNetworkDiff(&buf, old, new)
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "Network allow:")
	assert.Contains(t, buf.String(), "+ b.com")
}

// --- printDirAdditions ---

func TestPrintDirAdditions_NoAdditions(t *testing.T) {
	var buf bytes.Buffer
	dirs := []sandbox.ProfileDir{{Path: "/a"}}
	printed := printDirAdditions(&buf, dirs, dirs)
	assert.False(t, printed)
}

func TestPrintDirAdditions_NewDirs(t *testing.T) {
	var buf bytes.Buffer
	old := []sandbox.ProfileDir{{Path: "/a"}}
	new := []sandbox.ProfileDir{{Path: "/a"}, {Path: "/b", Mode: "rw"}}
	printed := printDirAdditions(&buf, old, new)
	assert.True(t, printed)
	assert.Contains(t, buf.String(), "Directories:")
	assert.Contains(t, buf.String(), "+ /b (rw)")
}

// --- joinNames ---

func TestJoinNames_Empty(t *testing.T) {
	assert.Equal(t, "", joinNames(nil))
}

func TestJoinNames_Single(t *testing.T) {
	assert.Equal(t, "alpha", joinNames([]string{"alpha"}))
}

func TestJoinNames_Multiple(t *testing.T) {
	assert.Equal(t, "alpha, beta, gamma", joinNames([]string{"alpha", "beta", "gamma"}))
}
