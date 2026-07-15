package caps

// ABOUTME: Tests for RunChecks, ComputeAvailability, FormatError, and related logic.

import (
	"context"
	"errors"
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

// TestFormatError_IncludesDetailAndFixSteps is the UX contract: a fixable
// failure surfaces why it's needed and the remediation commands right there,
// plus a pointer to system doctor — the user shouldn't have to run doctor to
// learn how to fix it.
func TestFormatError_IncludesDetailAndFixSteps(t *testing.T) {
	results := []CheckResult{{
		Cap: HostCapability{
			Summary: "network namespace creation",
			Detail:  "VM isolation requires CAP_SYS_ADMIN to create named network namespaces.",
		},
		Err: errors.New("operation not permitted"),
		Steps: []FixStep{
			{Description: "Run as root (simplest)", Command: "sudo yoloai new mybox --isolation vm ...", NeedsRoot: true},
			{Description: "Grant capability to binary", Command: "sudo setcap cap_sys_admin+ep $(which yoloai)", NeedsRoot: true},
		},
	}}
	msg := FormatError(results).Error()
	assert.Contains(t, msg, "VM isolation requires CAP_SYS_ADMIN", "shows why it's needed")
	assert.Contains(t, msg, "to fix (choose one):", "labels the alternatives")
	assert.Contains(t, msg, "sudo setcap cap_sys_admin+ep $(which yoloai)", "shows the actual command")
	assert.Contains(t, msg, "yoloai system doctor", "points at fuller diagnostics")
}

// TestFormatError_PermanentShowsNoFixSteps: a permanent failure states why and
// that it can't be resolved here — no misleading fix commands.
func TestFormatError_PermanentShowsNoFixSteps(t *testing.T) {
	results := []CheckResult{{
		Cap:         HostCapability{Summary: "KVM device access", Detail: "Requires hardware virtualization."},
		Err:         errors.New("/dev/kvm not found"),
		IsPermanent: true,
		Steps:       []FixStep{{Description: "should not appear", Command: "do-not-show"}},
	}}
	msg := FormatError(results).Error()
	assert.Contains(t, msg, "cannot be resolved in the current environment")
	assert.NotContains(t, msg, "do-not-show")
}

// TestFormatError_SkipsAdvisory: an Advisory failure never blocks — FormatError
// must omit it entirely, even alongside a genuine blocking failure.
func TestFormatError_SkipsAdvisory(t *testing.T) {
	results := []CheckResult{
		{Cap: HostCapability{Summary: "runc version floor", Advisory: true}, Err: errors.New("runc 1.1.0 is below the floor")},
		{Cap: HostCapability{Summary: "KVM device access"}, Err: errors.New("not found")},
	}
	err := FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KVM device access")
	assert.NotContains(t, err.Error(), "runc version floor")
}

// TestFormatError_AllAdvisoryMeansNoError: if every failure is advisory,
// FormatError must not block at all.
func TestFormatError_AllAdvisoryMeansNoError(t *testing.T) {
	results := []CheckResult{
		{Cap: HostCapability{Summary: "runc version floor", Advisory: true}, Err: errors.New("below floor")},
	}
	assert.NoError(t, FormatError(results))
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
