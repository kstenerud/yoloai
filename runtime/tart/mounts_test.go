// ABOUTME: Tests for the pure, VM-free pieces of the Tart mount/setup subsystem —
// ABOUTME: currently the in-guest hostname-set command construction.
package tart

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstenerud/yoloai/internal/config"
)

func TestHostnameSetCommand_SetsAllThreeMacOSNames(t *testing.T) {
	cmd := hostnameSetCommand("my-sandbox")
	// All three macOS hostname facets must be set: HostName (what `hostname` and
	// shells read), LocalHostName (Bonjour/.local), and ComputerName (UI label).
	assert.Contains(t, cmd, "scutil --set HostName 'my-sandbox'")
	assert.Contains(t, cmd, "scutil --set LocalHostName 'my-sandbox'")
	assert.Contains(t, cmd, "scutil --set ComputerName 'my-sandbox'")
	// Chained with && so a failure short-circuits and runTart surfaces it.
	assert.Equal(t, 2, strings.Count(cmd, "&&"))
}

func TestHostnameSetCommand_AcceptsSanitizedLabel(t *testing.T) {
	// The orchestrator feeds a config.SanitizeHostname'd value, which contains no
	// shell metacharacters, so single-quoting is sufficient and the label is a
	// valid LocalHostName (a DNS label). Guard that assumption end to end.
	label := config.SanitizeHostname("My_Feature.Branch")
	assert.Equal(t, "my-feature-branch", label)
	cmd := hostnameSetCommand(label)
	assert.NotContains(t, label, "'")
	assert.Contains(t, cmd, "'my-feature-branch'")
}
