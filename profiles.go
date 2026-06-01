// ABOUTME: SystemClient.Profiles() sub-handle: profile create/list/info/delete
// ABOUTME: as library orchestration; CLI consumes the typed results.

package yoloai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// profileScaffold is the commented config.yaml template written for
// new profiles. Kept here (not in internal/config) because it's a
// presentation concern of "what does a fresh profile look like" and
// owned by the user-facing Create surface.
const profileScaffold = `# agent: claude
# model: sonnet
# backend: docker   # optional backend constraint
# os: linux         # guest OS: linux, mac
# tart:
#   image: my-vm    # Tart backend only
# ports:
#   - "8080:8080"
# env:
#   MY_VAR: value
# mounts:                    # extra bind mounts (host:container[:ro])
#   - ~/.gitconfig:/home/yoloai/.gitconfig:ro
# network:
#   isolated: true           # enable network isolation
#   allow:                   # domains allowed when isolated
#     - example.com
# workdir:
#   path: ~/my-project
#   mode: copy       # copy or rw
#   mount: /opt/app  # optional custom mount point
# directories:
#   - path: ~/shared-lib
#     mode: rw
#     mount: /usr/local/lib/shared
`

// ProfileAdmin is the SystemClient sub-handle for profile-management
// operations (`yoloai profile create/list/info/delete`).
type ProfileAdmin struct {
	s *SystemClient
}

// Profiles returns the profile-management sub-handle.
//
// Q-W resolution (Shape B, sub-handles): profile admin is grouped
// behind one accessor so the SystemClient root stays uncluttered as
// admin verbs grow. Mirrors the same pattern Config() uses.
func (s *SystemClient) Profiles() *ProfileAdmin {
	return &ProfileAdmin{s: s}
}

// Create scaffolds a new profile directory under ~/.yoloai/profiles/<name>/
// with a commented config.yaml template.
//
// Returns a *UsageError if the name is invalid or the profile already
// exists (Q-B taxonomy: name-validation and existence collisions are
// user-actionable input errors).
func (a *ProfileAdmin) Create(_ context.Context, name string) error {
	if err := config.ValidateProfileName(name); err != nil {
		return err
	}
	if config.ProfileExists(a.s.layout, name) {
		return yoerrors.NewUsageError("profile %q already exists", name)
	}

	dir := a.s.layout.ProfileDir(name)
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	yamlPath := filepath.Join(dir, "config.yaml")
	if err := fileutil.WriteFile(yamlPath, []byte(profileScaffold), 0600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}
	return nil
}

// ProfileSummary is a row in ProfileAdmin.List output.
type ProfileSummary struct {
	Name          string    // profile name
	Agent         AgentName // configured agent, empty if not set
	HasDockerfile bool      // profile carries its own Dockerfile
}

// List returns one ProfileSummary per profile under ~/.yoloai/profiles/.
// The slice is sorted by name (lexicographic). Returns an empty slice
// (not nil) when no profiles are configured, so JSON output renders
// `[]` rather than `null`.
func (a *ProfileAdmin) List(_ context.Context) ([]ProfileSummary, error) {
	names, err := config.ListProfiles(a.s.layout)
	if err != nil {
		return nil, err
	}
	out := make([]ProfileSummary, 0, len(names))
	for _, name := range names {
		summary := ProfileSummary{
			Name:          name,
			HasDockerfile: config.ProfileHasDockerfile(a.s.layout, name),
		}
		if profile, loadErr := config.LoadProfile(a.s.layout, name); loadErr == nil {
			summary.Agent = AgentName(profile.Agent)
		}
		out = append(out, summary)
	}
	return out, nil
}

// ProfileInfo is the full information for a single profile, including
// resolved inheritance chain and merged configuration.
type ProfileInfo struct {
	Name          string
	Chain         []string               // resolved inheritance chain (1 element since profiles don't currently inherit; kept for forward-compat)
	Image         string                 // backend image name (e.g. "yoloai-myprofile" or "yoloai-base")
	HasDockerfile bool                   // profile carries its own Dockerfile
	Merged        *ResolvedProfileConfig // fully merged config
	// Parent is the merged config of the parent chain (chain[:-1]).
	// Non-nil; for "base" this is the zero-value config.
	// Used by callers that need to compute the profile's own additions
	// (the CLI's `profile info --diff` mode).
	Parent *ResolvedProfileConfig
}

// Info returns full information for a profile, including the resolved
// merged config and (for `--diff` consumers) the parent chain's merged
// config. The special name "base" returns the baked-in defaults.
//
// Returns a *UsageError if the name is invalid or does not exist.
func (a *ProfileAdmin) Info(_ context.Context, name string) (*ProfileInfo, error) {
	if name == "base" {
		return a.infoBase()
	}
	if err := config.ValidateProfileName(name); err != nil {
		return nil, err
	}
	if !config.ProfileExists(a.s.layout, name) {
		return nil, yoerrors.NewUsageError("profile %q does not exist", name)
	}
	chain, err := config.ResolveProfileChain(a.s.layout, name)
	if err != nil {
		return nil, err
	}
	baseCfg, err := config.LoadBakedInDefaults()
	if err != nil {
		return nil, err
	}
	merged, err := config.MergeProfileChain(a.s.layout, baseCfg, chain)
	if err != nil {
		return nil, err
	}

	// Parent merged config: everything before the leaf in the chain.
	// For a single-element chain (no inheritance), that's the baked-in
	// defaults alone.
	parentChain := chain[:len(chain)-1]
	parent, err := config.MergeProfileChain(a.s.layout, baseCfg, parentChain)
	if err != nil {
		return nil, err
	}

	return &ProfileInfo{
		Name:          name,
		Chain:         chain,
		Image:         config.ResolveProfileImage(a.s.layout, name, chain),
		HasDockerfile: config.ProfileHasDockerfile(a.s.layout, name),
		Merged:        resolvedProfileConfigFromMerged(merged),
		Parent:        resolvedProfileConfigFromMerged(parent),
	}, nil
}

// infoBase is the "base" branch of Info() — the only profile with
// no on-disk directory, since it's the baked-in defaults.
func (a *ProfileAdmin) infoBase() (*ProfileInfo, error) {
	chain := []string{"base"}
	baseCfg, err := config.LoadBakedInDefaults()
	if err != nil {
		return nil, err
	}
	merged, err := config.MergeProfileChain(a.s.layout, baseCfg, chain)
	if err != nil {
		return nil, err
	}
	return &ProfileInfo{
		Name:          "base",
		Chain:         chain,
		Image:         "yoloai-base",
		HasDockerfile: config.ProfileHasDockerfile(a.s.layout, "base"),
		Merged:        resolvedProfileConfigFromMerged(merged),
		// "base" has no parent; an empty config lets diff callers
		// treat it the same as any other profile without a nil-check.
		Parent: resolvedProfileConfigFromMerged(&config.MergedConfig{}),
	}, nil
}

// ReferencingSandboxes returns the names of sandboxes whose environment.json
// records this profile. Used by the CLI's `profile delete` flow to
// warn the user before removing a profile that's still in use.
//
// Errors during meta scanning are silently dropped per-entry — a
// corrupt environment.json shouldn't block the caller's profile-management
// operation. Returns an empty (non-nil) slice when no references exist.
func (a *ProfileAdmin) ReferencingSandboxes(_ context.Context, profileName string) ([]string, error) {
	sandboxesDir := a.s.layout.SandboxesDir()
	entries, err := os.ReadDir(sandboxesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read sandboxes dir: %w", err)
	}

	refs := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(sandboxesDir, e.Name(), store.EnvironmentFile)
		data, readErr := os.ReadFile(metaPath) //nolint:gosec // G304: path derived from sandboxes dir
		if readErr != nil {
			continue
		}
		var meta struct {
			Profile string `json:"profile"`
		}
		if json.Unmarshal(data, &meta) == nil && meta.Profile == profileName {
			refs = append(refs, e.Name())
		}
	}
	return refs, nil
}

// ImageCleanupHint describes a per-backend command for removing a
// profile's built image. Returned by ProfileAdmin.Delete so the CLI
// can surface them after the profile directory is gone.
type ImageCleanupHint struct {
	Backend BackendName // backend descriptor name (yoloai.BackendDocker, etc.)
	Image   string      // e.g. "yoloai-myprofile"
	Command string      // suggested shell command
}

// DeleteProfileResult is the structured return from ProfileAdmin.Delete.
type DeleteProfileResult struct {
	// ImageCleanupHints — one entry per registered backend that builds
	// per-profile images. The CLI prints these as "Note: if a docker
	// image 'yoloai-myprofile' exists, remove it with: docker rmi …".
	// Empty when no backend in the registry declares a CleanupHint.
	ImageCleanupHints []ImageCleanupHint
}

// Delete removes the profile directory. Returns a *UsageError if the
// name is invalid or the profile does not exist.
//
// Caller is responsible for any UX-level safety: this method does NOT
// check for sandboxes that reference the profile and does NOT prompt.
// CLI flow is: call ReferencingSandboxes → surface the warning →
// prompt → call Delete. Library callers wiring up automation can skip
// the prompt and accept the consequence (the referenced sandboxes
// still work; they just lose their profile reference).
func (a *ProfileAdmin) Delete(_ context.Context, name string) (*DeleteProfileResult, error) {
	if err := config.ValidateProfileName(name); err != nil {
		return nil, err
	}
	if !config.ProfileExists(a.s.layout, name) {
		return nil, yoerrors.NewUsageError("profile %q does not exist", name)
	}

	dir := a.s.layout.ProfileDir(name)
	if err := os.RemoveAll(dir); err != nil { //nolint:gosec // G703: dir derived from validated name
		return nil, fmt.Errorf("remove profile directory: %w", err)
	}

	image := "yoloai-" + name
	hints := make([]ImageCleanupHint, 0)
	for _, desc := range runtime.Descriptors() {
		if desc.CleanupHint == nil {
			continue
		}
		hints = append(hints, ImageCleanupHint{
			Backend: BackendName(desc.Name),
			Image:   image,
			Command: desc.CleanupHint(image),
		})
	}
	return &DeleteProfileResult{ImageCleanupHints: hints}, nil
}
