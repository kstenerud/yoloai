package caps

// ABOUTME: Tests for RunChecks, ComputeAvailability, FormatError, and related logic.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- RunChecks tests ---

func TestRunChecks_AllPass(t *testing.T) {
	caps := []HostCapability{
		{ID: "a", Summary: "Cap A", Check: func(_ context.Context) error { return nil }},
		{ID: "b", Summary: "Cap B", Check: func(_ context.Context) error { return nil }},
	}
	env := Environment{}
	results := RunChecks(context.Background(), caps, env)
	require.Len(t, results, 2)
	assert.NoError(t, results[0].Err)
	assert.NoError(t, results[1].Err)
}

func TestRunChecks_OneFailFixable(t *testing.T) {
	fixable := errors.New("fixable error")
	fixStep := FixStep{Description: "run this", Command: "apt install foo", NeedsRoot: true}
	caps := []HostCapability{
		{
			ID:        "a",
			Summary:   "Cap A",
			Check:     func(_ context.Context) error { return fixable },
			Permanent: func(_ Environment) bool { return false },
			Fix:       func(_ Environment) []FixStep { return []FixStep{fixStep} },
		},
	}
	env := Environment{}
	results := RunChecks(context.Background(), caps, env)
	require.Len(t, results, 1)
	assert.Error(t, results[0].Err)
	assert.False(t, results[0].IsPermanent)
	require.Len(t, results[0].Steps, 1)
	assert.Equal(t, fixStep, results[0].Steps[0])
}

func TestRunChecks_OneFailPermanent(t *testing.T) {
	caps := []HostCapability{
		{
			ID:        "a",
			Summary:   "Cap A",
			Check:     func(_ context.Context) error { return errors.New("permanent error") },
			Permanent: func(_ Environment) bool { return true },
		},
	}
	env := Environment{}
	results := RunChecks(context.Background(), caps, env)
	require.Len(t, results, 1)
	assert.Error(t, results[0].Err)
	assert.True(t, results[0].IsPermanent)
	assert.Nil(t, results[0].Steps) // no steps for permanent failures
}

func TestRunChecks_NilCheckIsPass(t *testing.T) {
	caps := []HostCapability{
		{ID: "a", Summary: "Cap A", Check: nil},
	}
	results := RunChecks(context.Background(), caps, Environment{})
	require.Len(t, results, 1)
	assert.NoError(t, results[0].Err)
}

func TestRunChecks_NilPermanentMeansFixable(t *testing.T) {
	fixStep := FixStep{Description: "install it"}
	caps := []HostCapability{
		{
			ID:        "a",
			Summary:   "Cap A",
			Check:     func(_ context.Context) error { return errors.New("oops") },
			Permanent: nil, // nil → assumed fixable
			Fix:       func(_ Environment) []FixStep { return []FixStep{fixStep} },
		},
	}
	results := RunChecks(context.Background(), caps, Environment{})
	require.Len(t, results, 1)
	assert.False(t, results[0].IsPermanent)
	assert.Len(t, results[0].Steps, 1)
}

// --- ComputeAvailability tests ---

func TestComputeAvailability_AllPass(t *testing.T) {
	results := []CheckResult{
		{Err: nil},
		{Err: nil},
	}
	assert.Equal(t, Ready, ComputeAvailability(results))
}

func TestComputeAvailability_FixableFailure(t *testing.T) {
	results := []CheckResult{
		{Err: nil},
		{Err: errors.New("fixable"), IsPermanent: false},
	}
	assert.Equal(t, NeedsSetup, ComputeAvailability(results))
}

func TestComputeAvailability_PermanentFailure(t *testing.T) {
	results := []CheckResult{
		{Err: nil},
		{Err: errors.New("permanent"), IsPermanent: true},
	}
	assert.Equal(t, Unavailable, ComputeAvailability(results))
}

func TestComputeAvailability_MixedPermanentAndFixable(t *testing.T) {
	// Permanent takes precedence over fixable.
	results := []CheckResult{
		{Err: errors.New("fixable"), IsPermanent: false},
		{Err: errors.New("permanent"), IsPermanent: true},
	}
	assert.Equal(t, Unavailable, ComputeAvailability(results))
}

func TestComputeAvailability_Empty(t *testing.T) {
	assert.Equal(t, Ready, ComputeAvailability(nil))
}

// --- FormatError tests ---

func TestFormatError_AllPass(t *testing.T) {
	results := []CheckResult{{Err: nil}, {Err: nil}}
	assert.NoError(t, FormatError(results))
}

func TestFormatError_SingleFailure(t *testing.T) {
	results := []CheckResult{
		{Cap: HostCapability{Summary: "KVM device"}, Err: errors.New("not found")},
	}
	err := FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KVM device")
	assert.Contains(t, err.Error(), "not found")
}

func TestFormatError_MultipleFailures(t *testing.T) {
	results := []CheckResult{
		{Cap: HostCapability{Summary: "Cap A"}, Err: errors.New("err1")},
		{Cap: HostCapability{Summary: "Cap B"}, Err: nil},
		{Cap: HostCapability{Summary: "Cap C"}, Err: errors.New("err3")},
	}
	err := FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cap A")
	assert.Contains(t, err.Error(), "Cap C")
	assert.NotContains(t, err.Error(), "Cap B")
}

// --- NewGVisorRunsc tests ---

func TestNewGVisorRunsc_Found(t *testing.T) {
	cap := NewGVisorRunsc(func(name string) (string, error) {
		return "/usr/local/sbin/runsc", nil
	})
	assert.Equal(t, "gvisor-runsc", cap.ID)
	err := cap.Check(context.Background())
	assert.NoError(t, err)
}

func TestNewGVisorRunsc_NotFound(t *testing.T) {
	cap := NewGVisorRunsc(func(name string) (string, error) {
		return "", errors.New("not found in PATH")
	})
	err := cap.Check(context.Background())
	assert.Error(t, err)
}

func TestNewGVisorRunsc_PermanentInContainer(t *testing.T) {
	cap := NewGVisorRunsc(func(string) (string, error) { return "", errors.New("x") })
	assert.True(t, cap.Permanent(Environment{InContainer: true}))
	assert.False(t, cap.Permanent(Environment{InContainer: false}))
}

func TestNewGVisorRunsc_FixSteps(t *testing.T) {
	cap := NewGVisorRunsc(func(string) (string, error) { return "", errors.New("x") })
	steps := cap.Fix(Environment{})
	require.Len(t, steps, 1)
	assert.Contains(t, steps[0].URL, "gvisor.dev")
	assert.True(t, steps[0].NeedsRoot)
}

// --- FormatDoctor tests (smoke) ---

func TestFormatDoctor_EmptyReports(t *testing.T) {
	var buf strings.Builder
	FormatDoctor(&buf, nil)
	assert.Contains(t, buf.String(), "No backends available")
}

func TestFormatDoctor_ReadyBackend(t *testing.T) {
	reports := []BackendReport{
		{
			Backend:      "docker",
			Mode:         "container",
			IsBaseMode:   true,
			Availability: Ready,
		},
	}
	var buf strings.Builder
	FormatDoctor(&buf, reports)
	out := buf.String()
	assert.Contains(t, out, "Ready to use")
	assert.Contains(t, out, "docker")
	assert.Contains(t, out, "container (default)")
}

func TestFormatDoctor_NeedsSetup(t *testing.T) {
	reports := []BackendReport{
		{
			Backend:    "containerd",
			Mode:       "vm",
			IsBaseMode: false,
			Results: []CheckResult{
				{Cap: HostCapability{Summary: "KVM device"}, Err: errors.New("not in kvm group"), IsPermanent: false, Steps: []FixStep{
					{Description: "Add to kvm group", Command: "sudo usermod -aG kvm $USER", NeedsRoot: true},
				}},
			},
			Availability: NeedsSetup,
		},
	}
	var buf strings.Builder
	FormatDoctor(&buf, reports)
	out := buf.String()
	assert.Contains(t, out, "Needs setup")
	assert.Contains(t, out, "containerd")
	assert.Contains(t, out, "To fix KVM device")
	assert.Contains(t, out, "usermod")
}

func TestFormatDoctor_Unavailable(t *testing.T) {
	reports := []BackendReport{
		{
			Backend:      "tart",
			Mode:         "vm",
			IsBaseMode:   true,
			InitErr:      errors.New("requires macOS with Apple Silicon"),
			Availability: Unavailable,
		},
	}
	var buf strings.Builder
	FormatDoctor(&buf, reports)
	out := buf.String()
	assert.Contains(t, out, "Not available")
	assert.Contains(t, out, "tart")
	assert.Contains(t, out, "requires macOS")
}
