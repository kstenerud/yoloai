// ABOUTME: The single choke point for spawning subprocesses. Every exec.Command
// ABOUTME: in yoloAI routes through here with an explicit env — never inherited.

// Package sysexec is the one licensed site for creating subprocesses. Go's
// exec.Command leaves Cmd.Env nil by default, which makes the child inherit the
// parent's full ambient environment (os.Environ()). DEV §12 forbids that: a
// child must read only the env we hand it, built from edge-resolved config. So
// every exec.Command/CommandContext call in the codebase goes through this
// package, which requires an explicit env; forbidigo bans the raw calls
// everywhere else (including _test.go — tests get no pass).
package sysexec

import (
	"context"
	"os/exec"
	"sort"
)

// CommandContext builds a context-bound *exec.Cmd whose environment is set
// explicitly to env. The caller configures Stdin/Stdout/Stderr/Dir and then
// calls Run/Output/Start as usual.
//
// env must be non-nil. Pass an empty slice for "no environment"; never nil —
// nil leaves Cmd.Env unset, which makes exec inherit the ambient os.Environ(),
// the exact leak DEV §12 exists to prevent.
func CommandContext(ctx context.Context, env []string, name string, args ...string) *exec.Cmd {
	requireEnv(env)
	//nolint:gosec // G204: sysexec is the single licensed subprocess site; name/args are caller-supplied from validated config (DEV §12).
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	return cmd
}

// Command is the non-context variant, for long-lived processes that must outlive
// a cancellable context and be stopped explicitly — e.g. `tart run`, which the
// tart backend kills directly rather than via context cancellation (see
// backend-idiosyncrasies: "tart run process must use exec.Command"). Same
// explicit-env contract as CommandContext.
func Command(env []string, name string, args ...string) *exec.Cmd {
	requireEnv(env)
	//nolint:gosec // G204: sysexec is the single licensed subprocess site; name/args are caller-supplied from validated config (DEV §12).
	cmd := exec.Command(name, args...)
	cmd.Env = env
	return cmd
}

func requireEnv(env []string) {
	if env == nil {
		panic("sysexec: explicit env required (pass an empty slice for none); a nil env makes exec inherit ambient os.Environ() — DEV §12")
	}
}

// Curated builds an explicit subprocess environment from the edge-resolved
// layout env. Only keys named in allow are carried through from layoutEnv;
// overrides are then applied and win over any allowlisted value. The result
// contains nothing not named in allow or overrides, so a child process can
// never pick up an ambient variable yoloAI did not choose to pass.
//
// The returned slice is sorted for determinism. It is always non-nil (an empty
// allow + empty overrides yields an empty, non-nil slice) so it can be handed
// straight to Command/CommandContext.
func Curated(layoutEnv map[string]string, allow []string, overrides map[string]string) []string {
	out := make([]string, 0, len(allow)+len(overrides))
	chosen := make(map[string]bool, len(overrides))
	for k, v := range overrides {
		out = append(out, k+"="+v)
		chosen[k] = true
	}
	for _, k := range allow {
		if chosen[k] {
			continue
		}
		if v, ok := layoutEnv[k]; ok {
			out = append(out, k+"="+v)
			chosen[k] = true
		}
	}
	sort.Strings(out)
	return out
}
