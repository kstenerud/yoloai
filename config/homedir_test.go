package config

import (
	"os"
	"testing"
)

func TestHomeDir(t *testing.T) {
	home := HomeDir()
	if home == "" {
		t.Fatal("HomeDir() returned empty string")
	}

	expected, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir() failed: %v", err)
	}
	if home != expected {
		t.Errorf("HomeDir() = %q, want %q", home, expected)
	}
}
