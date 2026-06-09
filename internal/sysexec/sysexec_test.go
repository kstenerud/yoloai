// ABOUTME: Tests for the sysexec choke point: explicit-env contract and the
// ABOUTME: Curated allowlist builder.

package sysexec

import (
	"context"
	"reflect"
	"testing"
)

func TestCommandContext_SetsExplicitEnv(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/tmp/x"}
	cmd := CommandContext(context.Background(), env, "echo", "hi")
	if !reflect.DeepEqual(cmd.Env, env) {
		t.Fatalf("Env = %v, want %v", cmd.Env, env)
	}
	if cmd.Args[0] != "echo" || cmd.Args[1] != "hi" {
		t.Fatalf("Args = %v", cmd.Args)
	}
}

func TestCommand_EmptyEnvMeansNoEnv(t *testing.T) {
	// An explicit empty (non-nil) env is the locked-down case: the child gets
	// nothing. It must NOT fall back to inheriting os.Environ().
	cmd := Command([]string{}, "true")
	if cmd.Env == nil {
		t.Fatal("Env became nil — would inherit ambient os.Environ()")
	}
	if len(cmd.Env) != 0 {
		t.Fatalf("Env = %v, want empty", cmd.Env)
	}
}

func TestRequireEnv_NilPanics(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func()
	}{
		{"CommandContext", func() { CommandContext(context.Background(), nil, "true") }},
		{"Command", func() { Command(nil, "true") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic on nil env (would inherit ambient os.Environ())")
				}
			}()
			tc.call()
		})
	}
}

func TestCurated(t *testing.T) {
	layoutEnv := map[string]string{
		"PATH":        "/usr/bin",
		"HOME":        "/home/real",
		"TART_HOME":   "/home/real/.tart",
		"SECRET":      "do-not-leak",
		"DOCKER_HOST": "unix:///real.sock",
	}
	got := Curated(
		layoutEnv,
		[]string{"PATH", "MISSING"}, // allow (MISSING absent from layoutEnv → dropped)
		map[string]string{"HOME": "/tmp/iso", "TART_HOME": "/tmp/iso/.tart"}, // overrides win
	)
	want := []string{"HOME=/tmp/iso", "PATH=/usr/bin", "TART_HOME=/tmp/iso/.tart"} // sorted; SECRET + DOCKER_HOST excluded
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Curated = %v, want %v", got, want)
	}
}

func TestCurated_OverrideWinsOverAllow(t *testing.T) {
	got := Curated(
		map[string]string{"HOME": "/home/real"},
		[]string{"HOME"},
		map[string]string{"HOME": "/tmp/iso"},
	)
	want := []string{"HOME=/tmp/iso"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Curated = %v, want %v", got, want)
	}
}

func TestCurated_EmptyIsNonNil(t *testing.T) {
	got := Curated(nil, nil, nil)
	if got == nil {
		t.Fatal("Curated returned nil; must be non-nil so it can pass requireEnv")
	}
	if len(got) != 0 {
		t.Fatalf("Curated = %v, want empty", got)
	}
}
