// ABOUTME: Codex config.toml/auth.json patches: the broker redirect (openai_base_url
// ABOUTME: + placeholder auth.json) and the launch-time workdir folder-trust (DF85).
package agent

import (
	"encoding/json"
	"fmt"

	toml "github.com/pelletier/go-toml/v2"
)

// patchCodexBaseURL points Codex's built-in openai provider at the broker
// injector by setting the top-level openai_base_url key in ~/.codex/config.toml.
// It parses the existing config (empty when no host config was seeded), sets the
// key, and re-encodes — preserving the user's other settings (model, approvals).
// baseURL is the injector root ("http://host:port"); Codex appends "/responses"
// to it, so a "/v1" suffix is added here to yield the real "/v1/responses" path
// once the injector forwards to api.openai.com. The placeholder is unused (it is
// delivered via auth.json, patchCodexAuth). Round-tripping through a map drops
// comments/formatting, which is acceptable: the target is the sandbox's throwaway
// config copy, not the user's real file.
func patchCodexBaseURL(current []byte, baseURL, _ string) ([]byte, error) {
	cfg := map[string]any{}
	if len(current) > 0 {
		if err := toml.Unmarshal(current, &cfg); err != nil {
			return nil, fmt.Errorf("agent: parse codex config.toml: %w", err)
		}
	}
	cfg["openai_base_url"] = baseURL + "/v1"
	out, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent: encode codex config.toml: %w", err)
	}
	return out, nil
}

// patchCodexAuth writes the placeholder API key into Codex's auth.json in the
// shape `codex login --with-api-key` produces — {auth_mode:"apikey", OPENAI_API_KEY:...}.
// Codex 0.144 does not authenticate from a bare OPENAI_API_KEY env var (it reports
// "not logged in"); it reads this file. Delivering the placeholder here — rather
// than an env var — is what lets the injector swap it for the real key. baseURL is
// unused (the redirect lives in config.toml). current is ignored: a brokered
// launch always overwrites auth.json with the placeholder (the seeded host
// auth.json, if any, is skipped when an API key is present).
func patchCodexAuth(_ []byte, _, placeholder string) ([]byte, error) {
	return renderCodexAuth(placeholder)
}

// renderCodexAuth builds a Codex auth.json for API-key auth, in the shape
// `codex login --with-api-key` produces. It is the direct-delivery analogue of
// patchCodexAuth: the launch path calls it with the REAL key when Codex is not
// brokered (a bare OPENAI_API_KEY env var doesn't authenticate Codex 0.144 —
// DF84), while patchCodexAuth calls it with the per-sandbox placeholder when it
// is brokered. Same file, different key.
func renderCodexAuth(key string) ([]byte, error) {
	out, err := json.MarshalIndent(map[string]any{
		"auth_mode":      "apikey",
		"OPENAI_API_KEY": key,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("agent: encode codex auth.json: %w", err)
	}
	return append(out, '\n'), nil
}

// patchCodexWorkdirTrust records the container working directory as a trusted
// project in Codex's config.toml. Codex 0.144 blocks on a "Do you trust this
// directory?" onboarding prompt for any workdir not marked trusted — the pasted
// prompt lands in that dialog and the agent exits (DF85). Codex has no global
// trust-disable; trust is per-project-root, so the launch path marks the exact
// workdir (the agent's cwd) trusted. It round-trips the TOML map — preserving the
// user's config and any broker openai_base_url patch — and is idempotent across
// relaunches (config.toml is re-seeded from the host each launch, so this must
// re-apply every time). Empty workdir is a no-op.
func patchCodexWorkdirTrust(current []byte, workdir string) ([]byte, error) {
	if workdir == "" {
		return current, nil
	}
	cfg := map[string]any{}
	if len(current) > 0 {
		if err := toml.Unmarshal(current, &cfg); err != nil {
			return nil, fmt.Errorf("agent: parse codex config.toml: %w", err)
		}
	}
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	entry, _ := projects[workdir].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["trust_level"] = "trusted"
	projects[workdir] = entry
	cfg["projects"] = projects
	out, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("agent: encode codex config.toml: %w", err)
	}
	return out, nil
}
