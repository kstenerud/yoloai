package doctorcmd

// ABOUTME: Smoke tests for formatDoctor — the human-readable doctor formatter
// ABOUTME: over the public yoloai.BackendReport read-model.

import (
	"errors"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
)

func TestFormatDoctor_EmptyReports(t *testing.T) {
	var buf strings.Builder
	formatDoctor(&buf, nil)
	assert.Contains(t, buf.String(), "No backends available")
}

func TestFormatDoctor_ReadyBackend(t *testing.T) {
	reports := []yoloai.BackendReport{
		{
			Type:         "docker",
			Mode:         "container",
			IsBaseMode:   true,
			Availability: yoloai.Ready,
		},
	}
	var buf strings.Builder
	formatDoctor(&buf, reports)
	out := buf.String()
	assert.Contains(t, out, "Ready to use")
	assert.Contains(t, out, "docker")
	assert.Contains(t, out, "container (default)")
}

func TestFormatDoctor_NeedsSetup(t *testing.T) {
	reports := []yoloai.BackendReport{
		{
			Type:       "containerd",
			Mode:       "vm",
			IsBaseMode: false,
			Results: []yoloai.CapabilityCheck{
				{Capability: yoloai.Capability{Summary: "KVM device"}, Err: errors.New("not in kvm group"), IsPermanent: false, Steps: []yoloai.FixStep{
					{Description: "Add to kvm group", Command: "sudo usermod -aG kvm $USER", NeedsRoot: true},
				}},
			},
			Availability: yoloai.NeedsSetup,
		},
	}
	var buf strings.Builder
	formatDoctor(&buf, reports)
	out := buf.String()
	assert.Contains(t, out, "Needs setup")
	assert.Contains(t, out, "containerd")
	assert.Contains(t, out, "To fix KVM device")
	assert.Contains(t, out, "usermod")
}

// A single fix step whose only payload is a Description (no command/URL) must
// still render its guidance — otherwise the "To fix" header prints with nothing
// under it (the KVM-not-detected case).
func TestFormatDoctor_DescriptionOnlyStepShown(t *testing.T) {
	reports := []yoloai.BackendReport{
		{
			Type:       "containerd",
			Mode:       "vm",
			IsBaseMode: false,
			Results: []yoloai.CapabilityCheck{
				{Capability: yoloai.Capability{Summary: "KVM device access"}, Err: errors.New("/dev/kvm not found"), IsPermanent: false, Steps: []yoloai.FixStep{
					{Description: "KVM hardware not detected — enable passthrough in your hypervisor"},
				}},
			},
			Availability: yoloai.NeedsSetup,
		},
	}
	var buf strings.Builder
	formatDoctor(&buf, reports)
	out := buf.String()
	assert.Contains(t, out, "To fix KVM device access")
	assert.Contains(t, out, "KVM hardware not detected")
}

func TestFormatDoctor_Unavailable(t *testing.T) {
	reports := []yoloai.BackendReport{
		{
			Type:         "tart",
			Mode:         "vm",
			IsBaseMode:   true,
			InitErr:      errors.New("requires macOS with Apple Silicon"),
			Availability: yoloai.Unavailable,
		},
	}
	var buf strings.Builder
	formatDoctor(&buf, reports)
	out := buf.String()
	assert.Contains(t, out, "Not available")
	assert.Contains(t, out, "tart")
	assert.Contains(t, out, "requires macOS")
}
