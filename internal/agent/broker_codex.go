// ABOUTME: Codex broker file patches — config.toml openai_base_url (the redirect)
// ABOUTME: and auth.json (the placeholder API key), the two files Codex reads (D115).
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
	out, err := json.MarshalIndent(map[string]any{
		"auth_mode":      "apikey",
		"OPENAI_API_KEY": placeholder,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("agent: encode codex auth.json: %w", err)
	}
	return append(out, '\n'), nil
}
