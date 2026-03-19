package config

import (
	"os"
	"os/user"
	"testing"
)

func TestHomeDir_normal(t *testing.T) {
	// Without SUDO_USER, HomeDir should match os.UserHomeDir.
	_ = os.Unsetenv("SUDO_USER")
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

func TestHomeDir_sudoUser(t *testing.T) {
	// When SUDO_USER is set to a real user and we can look them up,
	// HomeDir should return that user's home directory.
	// We use the current user as SUDO_USER to get a predictable home dir.
	cur, err := user.Current()
	if err != nil {
		t.Skip("cannot look up current user:", err)
	}
	t.Setenv("SUDO_USER", cur.Username)

	home := HomeDir()
	if home != cur.HomeDir {
		t.Errorf("HomeDir() with SUDO_USER=%q = %q, want %q", cur.Username, home, cur.HomeDir)
	}
}
