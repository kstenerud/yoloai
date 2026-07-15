package caps

// ABOUTME: Tests for NewOCIRuntimeVersionFloor — the shared runc/crun version-floor
// ABOUTME: capability constructor.

import (
	"context"
	"errors"
	goruntime "runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func meetsFloor_1_20(major, minor, _ int) bool {
	if major != 1 {
		return major > 1
	}
	return minor >= 20
}

func TestNewOCIRuntimeVersionFloor_AboveFloor(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("runtime-version check only runs on Linux")
	}
	cap := NewOCIRuntimeVersionFloor(
		"crun-version-floor", "crun", "crun version floor", "detail", "https://example.com/install",
		func(string) (string, error) { return "/usr/bin/crun", nil },
		func(string) ([]byte, error) { return []byte("crun version 1.20.0\ncommit: abc\n"), nil },
		meetsFloor_1_20,
	)
	assert.True(t, cap.Advisory)
	assert.NoError(t, cap.Check(context.Background()))
}

func TestNewOCIRuntimeVersionFloor_BelowFloor(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("runtime-version check only runs on Linux")
	}
	cap := NewOCIRuntimeVersionFloor(
		"crun-version-floor", "crun", "crun version floor", "detail", "https://example.com/install",
		func(string) (string, error) { return "/usr/bin/crun", nil },
		func(string) ([]byte, error) { return []byte("crun version 1.19.0\n"), nil },
		meetsFloor_1_20,
	)
	err := cap.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "below the recommended version floor")
}

func TestNewOCIRuntimeVersionFloor_MalformedVersion(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("runtime-version check only runs on Linux")
	}
	cap := NewOCIRuntimeVersionFloor(
		"crun-version-floor", "crun", "crun version floor", "detail", "https://example.com/install",
		func(string) (string, error) { return "/usr/bin/crun", nil },
		func(string) ([]byte, error) { return []byte("not a version string\n"), nil },
		meetsFloor_1_20,
	)
	err := cap.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not parse")
}

func TestNewOCIRuntimeVersionFloor_BinaryNotOnPath(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("runtime-version check only runs on Linux")
	}
	cap := NewOCIRuntimeVersionFloor(
		"crun-version-floor", "crun", "crun version floor", "detail", "https://example.com/install",
		func(string) (string, error) { return "", errors.New("not found") },
		func(string) ([]byte, error) {
			t.Fatal("runVersion should not be called when lookPath fails")
			return nil, nil
		},
		meetsFloor_1_20,
	)
	// Not this check's job to flag a missing binary — other capabilities cover
	// backend/binary presence.
	assert.NoError(t, cap.Check(context.Background()))
}

func TestNewOCIRuntimeVersionFloor_RunVersionFails(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("runtime-version check only runs on Linux")
	}
	cap := NewOCIRuntimeVersionFloor(
		"crun-version-floor", "crun", "crun version floor", "detail", "https://example.com/install",
		func(string) (string, error) { return "/usr/bin/crun", nil },
		func(string) ([]byte, error) { return nil, errors.New("exec failed") },
		meetsFloor_1_20,
	)
	err := cap.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run crun --version")
}

func TestNewOCIRuntimeVersionFloor_FixSteps(t *testing.T) {
	cap := NewOCIRuntimeVersionFloor(
		"crun-version-floor", "crun", "crun version floor", "detail", "https://example.com/install",
		func(string) (string, error) { return "/usr/bin/crun", nil },
		func(string) ([]byte, error) { return []byte("crun version 1.19.0\n"), nil },
		meetsFloor_1_20,
	)
	steps := cap.Fix(Environment{})
	require.Len(t, steps, 1)
	assert.Equal(t, "https://example.com/install", steps[0].URL)
	assert.True(t, steps[0].NeedsRoot)
}
