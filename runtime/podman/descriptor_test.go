// ABOUTME: Tests that Descriptor() fields stay consistent with the individual interface methods.
package podman

import (
	"reflect"
	"testing"
)

// TestDescriptor_ConsistentWithMethods verifies that Descriptor() returns values
// that match Podman's individual interface method overrides. Guards against drift
// while both APIs coexist during the W11 migration.
//
// Note: Podman's Descriptor() intentionally hardcodes Name="podman" rather than
// delegating to the embedded docker.Runtime.Name() (which returns the binaryName
// field). The individual Name() method is not tested here for that reason; the
// hardcoded value is the authoritative source for the "podman" backend.
func TestDescriptor_ConsistentWithMethods(t *testing.T) {
	r := &Runtime{}

	d := r.Descriptor()

	if d.Name != "podman" {
		t.Errorf("Descriptor.Name = %q, want %q", d.Name, "podman")
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
