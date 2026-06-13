// ABOUTME: Public build-secret helpers (F1): detect well-known host credential
// ABOUTME: files and validate Docker BuildKit --secret specs for profile builds.
package yoloai

import "github.com/kstenerud/yoloai/internal/orchestrator/profiles"

// AutoBuildSecrets detects well-known credential files on the host (e.g.
// ~/.npmrc) and returns Docker BuildKit --secret specs (id=<name>,src=<path>)
// to forward into a profile image build. homeDir is the host home directory;
// callers must supply it explicitly (the library never reads $HOME — see
// development-principles.md §12). Returns nil when nothing is detected.
func AutoBuildSecrets(homeDir string) []string {
	return profiles.AutoBuildSecrets(homeDir)
}

// ValidateBuildSecret validates a Docker BuildKit --secret spec string of the
// form "id=<name>,src=<path>" (fields in either order). It expands ~ in src
// (using homeDir) and confirms the source file exists, returning the
// canonicalized "id=<name>,src=<abs-path>" spec. homeDir is the host home
// directory; the library never reads $HOME (development-principles.md §12).
func ValidateBuildSecret(spec, homeDir string) (string, error) {
	return profiles.ValidateBuildSecret(spec, homeDir)
}
