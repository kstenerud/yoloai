// ABOUTME: Tests that Descriptor() fields stay consistent with the individual interface methods.
package tart

import (
	"reflect"
	"testing"
)

// TestDescriptor_ConsistentWithMethods verifies that Descriptor() returns values
// that match the individual Name(), BaseModeName(), AgentProvisionedByBackend(),
// SupportedIsolationModes(), and Capabilities() methods. This guards against
// drift while both APIs coexist during the W11 migration.
func TestDescriptor_ConsistentWithMethods(t *testing.T) {
	r := &Runtime{}
	d := r.Descriptor()

	if d.Name != r.Name() {
		t.Errorf("Descriptor.Name = %q, Name() = %q", d.Name, r.Name())
	}
	if d.BaseModeName != r.BaseModeName() {
		t.Errorf("Descriptor.BaseModeName = %q, BaseModeName() = %q", d.BaseModeName, r.BaseModeName())
	}
	if d.AgentProvisionedByBackend != r.AgentProvisionedByBackend() {
		t.Errorf("Descriptor.AgentProvisionedByBackend = %v, AgentProvisionedByBackend() = %v",
			d.AgentProvisionedByBackend, r.AgentProvisionedByBackend())
	}
	if !reflect.DeepEqual(d.SupportedIsolationModes, r.SupportedIsolationModes()) {
		t.Errorf("Descriptor.SupportedIsolationModes = %v, SupportedIsolationModes() = %v",
			d.SupportedIsolationModes, r.SupportedIsolationModes())
	}
	if d.Capabilities != r.Capabilities() {
		t.Errorf("Descriptor.Capabilities = %+v, Capabilities() = %+v", d.Capabilities, r.Capabilities())
	}
}
