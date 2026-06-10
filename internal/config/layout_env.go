// ABOUTME: Curated accessors over the Layout's threaded env snapshot. The raw
// ABOUTME: map is never handed out: callers allowlist (ExecEnv/CuratedEnv) or
// ABOUTME: look up one key at a time (LookupEnv) so no ambient var leaks (§12).

package config

import (
	"sort"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// EnvLookup is the read-only, key-at-a-time view of the threaded environment
// snapshot. ${VAR} expansion and agent-secret resolution must read arbitrary
// user-/agent-named keys, so they cannot use a fixed allowlist; instead they
// take this interface, which yields one value at a time and never the whole
// map. A consumer therefore can't dump or forward the full ambient snapshot —
// the curation the snapshot exists to enforce can't be bypassed (§12).
type EnvLookup interface {
	LookupEnv(key string) (string, bool)
}

// LookupEnv reports the value of a single environment variable from the
// snapshot. It is the contained, arbitrary-key escape hatch for the two
// consumers that genuinely need it (${VAR} expansion and agent secrets);
// allowlistable consumers use ExecEnv/CuratedEnv instead.
func (l Layout) LookupEnv(key string) (string, bool) {
	v, ok := l.env[key]
	return v, ok
}

// ExecEnv builds an explicit subprocess environment from the snapshot: only
// the allowlisted vars are carried, plus overrides (which win over any
// allowlisted value). The result contains nothing not named in allow or
// overrides, so a child process can never inherit an ambient variable yoloAI
// did not choose to pass (§12). The slice is sorted and non-nil, ready for
// sysexec.Command.
func (l Layout) ExecEnv(allow []string, overrides map[string]string) []string {
	return sysexec.Curated(l.env, allow, overrides)
}

// GitEnv is the shared curated environment for host-side git subprocesses
// (PATH/HOME/TMPDIR/SUDO_UID). Kept as a named method so every host git call
// stays consistent; see sysexec.GitEnv for the SUDO_UID rationale.
func (l Layout) GitEnv() []string {
	return sysexec.GitEnv(l.env)
}

// CuratedEnv returns the allowlisted subset of the snapshot as a map, for the
// few consumers that need a map rather than an exec slice — the Docker SDK
// client config and Podman socket discovery, which index daemon keys directly.
// Like ExecEnv it never exposes a var outside allow. The result is always
// non-nil.
func (l Layout) CuratedEnv(allow []string) map[string]string {
	out := make(map[string]string, len(allow))
	for _, k := range allow {
		if v, ok := l.env[k]; ok {
			out[k] = v
		}
	}
	return out
}

// EnvForExtension returns the entire snapshot as a sorted KEY=VALUE slice. It
// is the ONE sanctioned full-passthrough: `yoloai x` runs user-authored
// extension scripts via `sh -c`, and those scripts get the user's full
// edge-resolved environment by design — the same env they'd see running the
// command in their own shell. It is deliberately named (not a generic getter)
// so this single legitimate use stays greppable; library code that shells out
// must use ExecEnv with an allowlist instead, never this. The snapshot is still
// the threaded edge capture, never os.Environ (§12).
func (l Layout) EnvForExtension() []string {
	out := make([]string, 0, len(l.env))
	for k, v := range l.env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// WithEnv returns a copy of the Layout carrying env as its snapshot. It is the
// only way to populate the (unexported) snapshot, so the capture stays at the
// edges: the CLI's licensed os.Environ read, yoloai.NewClient bridging the
// public ClientCreateOptions.Env, and tests. Library code receives a populated
// Layout and reads it through ExecEnv/CuratedEnv/LookupEnv — never sets it.
func (l Layout) WithEnv(env map[string]string) Layout {
	l.env = env
	return l
}

// EnvSnapshot returns a copy of the full snapshot as a map. It is the embedder/
// diagnostics boundary getter — the inverse of WithEnv — used only to forward
// the captured env across the public API (an embedder filling
// ClientCreateOptions.Env from its own Layout) or to dump it in a bug report.
// Library code that builds a subprocess environment must NOT use this; it must
// allowlist via ExecEnv/CuratedEnv so no ambient var leaks (§12). A copy is
// returned so a caller can't mutate the Layout's snapshot.
func (l Layout) EnvSnapshot() map[string]string {
	if l.env == nil {
		return nil
	}
	out := make(map[string]string, len(l.env))
	for k, v := range l.env {
		out[k] = v
	}
	return out
}

// MapEnv adapts a plain map to EnvLookup, for tests and edge construction that
// hold a literal env map rather than a Layout.
type MapEnv map[string]string

// LookupEnv satisfies EnvLookup.
func (m MapEnv) LookupEnv(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}
