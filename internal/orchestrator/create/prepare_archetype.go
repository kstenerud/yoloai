// ABOUTME: Archetype resolution and expansion pipeline — resolves the active
// ABOUTME: archetype (CLI flag or auto-detect), validates platform
// ABOUTME: requirements, and expands archetype effects onto opts and profileResult.
package create

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/archetype"
	mountspkg "github.com/kstenerud/yoloai/internal/orchestrator/mounts"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
)

// resolveAndApplyArchetype resolves the archetype with priority (CLI > auto-detect),
// validates platform requirements, expands archetype effects on opts and pr, and
// prints transparency output.
//
// Returns: (resolved archetype, devcontainer config, safe devcontainer mounts, mount warnings, error).
func resolveAndApplyArchetype(ctx context.Context, d state.Deps, opts *Options, pr *profileResult) (archetype.Archetype, *archetype.DevcontainerConfig, []string, []feedback.Notice, error) {
	workdir := opts.Workdir.Path

	warnIfYoloAIYamlPresent(opts.Notices, workdir)

	arch, signals, source, err := resolveArchetype(opts, workdir)
	if err != nil {
		return "", nil, nil, nil, err
	}

	// Step 2: Platform check for apple archetype
	if err := checkAppleArchetype(opts.Notices, arch, opts.Archetype); err != nil {
		return "", nil, nil, nil, err
	}

	// Step 3: Archetype expansion
	devcontainerCfg, dcMounts, dcMountNotices, bullets, err := expandArchetype(ctx, d, opts, pr, arch)
	if err != nil {
		return "", nil, nil, nil, err
	}

	// Step 4: Transparency output
	reportArchetype(opts.Notices, arch, source, signals, bullets)

	return arch, devcontainerCfg, dcMounts, dcMountNotices, nil
}

// warnIfYoloAIYamlPresent warns that a workdir's .yoloai.yaml is no longer read.
// D140 removed the .yoloai.yaml project-config feature; a repo that still has one
// (most likely for its mounts: key) needs to learn its effect is gone rather than
// losing host mounts silently. This is an existence check only — the file is never
// parsed.
func warnIfYoloAIYamlPresent(sink feedback.Sink, workdir string) {
	if _, err := os.Stat(filepath.Join(workdir, ".yoloai.yaml")); err != nil {
		return
	}
	feedback.Warnf(sink, "config.legacy_file_ignored",
		".yoloai.yaml is no longer read and is ignored (archetype/mounts/requires: all removed, D140). Use --archetype and devcontainer.json's mounts instead.")
}

// resolveArchetype determines the archetype from CLI or auto-detection.
func resolveArchetype(opts *Options, workdir string) (archetype.Archetype, []string, string, error) {
	switch {
	case opts.Archetype != "":
		a, err := archetype.ParseArchetype(opts.Archetype)
		if err != nil {
			return "", nil, "", err
		}
		return a, nil, "--archetype flag", nil
	default:
		arch, signals := archetype.DetectArchetype(workdir)
		return arch, signals, "auto-detected", nil
	}
}

// checkAppleArchetype validates platform requirements for the apple archetype.
func checkAppleArchetype(sink feedback.Sink, arch archetype.Archetype, cliArchetype string) error {
	if arch != archetype.ArchetypeApple {
		return nil
	}
	isAppleSilicon := goruntime.GOOS == "darwin" && goruntime.GOARCH == "arm64"
	if isAppleSilicon {
		return nil
	}
	if cliArchetype != "" {
		// Explicit --archetype apple on non-macOS → hard error
		return fmt.Errorf(
			"the \"apple\" archetype requires Apple Silicon macOS (Tart backend); " +
				"use --archetype simple for agent-only work on this project")
	}
	// Auto-detected apple on non-macOS → warn but don't fail
	feedback.Warnf(sink, "archetype.platform_unavailable",
		"This looks like an Apple platform project. The Tart backend requires Apple Silicon macOS.")
	return nil
}

// expandArchetype applies archetype-specific settings to opts and pr.
// Returns (devcontainerCfg, dcMounts, dcMountNotices, bullets, error).
func expandArchetype(ctx context.Context, d state.Deps, opts *Options, pr *profileResult, arch archetype.Archetype) (*archetype.DevcontainerConfig, []string, []feedback.Notice, []string, error) {
	var bullets []string
	var devcontainerCfg *archetype.DevcontainerConfig
	var dcMounts []string
	var dcMountNotices []feedback.Notice

	switch arch {
	case archetype.ArchetypeCompose:
		bullets = applyComposeArchetype(opts, pr)
	case archetype.ArchetypeDevcontainer:
		var err error
		devcontainerCfg, dcMounts, dcMountNotices, bullets, err = applyDevcontainerArchetype(ctx, d, opts, pr)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	case archetype.ArchetypeApple:
		bullets = append(bullets, "backend=tart required (Apple Silicon macOS VM)")
	case archetype.ArchetypeSimple:
		// no-op
	}

	return devcontainerCfg, dcMounts, dcMountNotices, bullets, nil
}

// applyComposeArchetype applies compose-specific settings to opts and pr.
func applyComposeArchetype(opts *Options, pr *profileResult) []string {
	var bullets []string
	// container-privileged is full host access; never auto-escalate to it from an
	// untrusted, auto-detected repo signal (a docker-compose file). Require the
	// user to have chosen it explicitly.
	if opts.Isolation != runtime.IsolationModeContainerPrivileged {
		bullets = append(bullets, "Compose requires nested Docker (container-privileged) — NOT auto-enabled: it grants full host access. Re-run with --isolation container-privileged if you trust this repo.")
		return bullets
	}
	pr.archetypeDockerDRequired = true
	bullets = append(bullets, "dockerd will auto-start before lifecycle commands")
	return bullets
}

// applyDevcontainerArchetype loads and applies devcontainer.json settings.
func applyDevcontainerArchetype(ctx context.Context, d state.Deps, opts *Options, pr *profileResult) (*archetype.DevcontainerConfig, []string, []feedback.Notice, []string, error) {
	_ = ctx // reserved for future use
	workdir := opts.Workdir.Path
	var bullets []string

	dcPath := findDevcontainerPath(workdir)
	if dcPath == "" {
		return nil, nil, nil, bullets, nil
	}

	dc, err := archetype.LoadDevcontainer(dcPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load devcontainer.json: %w", err)
	}

	if dc.DockerComposeFilePresent() {
		return nil, nil, nil, nil, fmt.Errorf(
			"docker Compose devcontainers are not supported; " +
				"use a project with devcontainer.json and docker-compose.yaml side by side instead")
	}

	dc.WarnIgnoredFields(opts.Notices)

	bullets = applyDevcontainerRunArgs(dc, pr, bullets, opts.Notices)
	bullets = applyDevcontainerCompose(dc, opts, pr, bullets)
	bullets = applyDevcontainerEnv(dc, pr, bullets)
	bullets = applyDevcontainerPorts(dc, opts, bullets)
	bullets = applyDevcontainerWorkspaceFolder(dc, opts, bullets)

	workdirMountPath := opts.Workdir.MountPath
	if workdirMountPath == "" {
		workdirMountPath = opts.Workdir.Path
	}
	dcMounts, dcMountNotices := dc.FilterMounts(workdirMountPath, d.Layout.HomeDir)
	if len(dcMounts) > 0 {
		bullets = append(bullets, fmt.Sprintf("%d devcontainer mounts passed through", len(dcMounts)))
	}

	bullets = appendLifecycleBullets(dc, bullets)

	return dc, dcMounts, dcMountNotices, bullets, nil
}

// findDevcontainerPath returns the path to devcontainer.json, or empty string if not found.
func findDevcontainerPath(workdir string) string {
	for _, candidate := range []string{
		filepath.Join(workdir, ".devcontainer", "devcontainer.json"),
		filepath.Join(workdir, "devcontainer.json"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// applyDevcontainerRunArgs applies runArgs (cpus, memory, capAdd) from devcontainer.json.
func applyDevcontainerRunArgs(dc *archetype.DevcontainerConfig, pr *profileResult, bullets []string, sink feedback.Sink) []string {
	cpus, memory, capAdd, notices := dc.ParsedRunArgs()
	for _, n := range notices {
		feedback.Emit(sink, n)
	}
	if cpus != "" && (pr.resources == nil || pr.resources.CPUs == "") {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.CPUs = cpus
		bullets = append(bullets, fmt.Sprintf("CPUs set to %s (from runArgs)", cpus))
	}
	if memory != "" && (pr.resources == nil || pr.resources.Memory == "") {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.Memory = memory
		bullets = append(bullets, fmt.Sprintf("memory set to %s (from runArgs)", memory))
	}
	pr.capAdd = append(pr.capAdd, capAdd...)
	return bullets
}

// applyDevcontainerCompose checks postStartCommand for compose usage and sets isolation.
func applyDevcontainerCompose(dc *archetype.DevcontainerConfig, opts *Options, pr *profileResult, bullets []string) []string {
	if !dc.PostStartCommandUsesCompose() {
		return bullets
	}
	// container-privileged is full host access; never auto-escalate to it from an
	// untrusted, auto-detected repo signal. Require explicit user opt-in.
	if opts.Isolation != runtime.IsolationModeContainerPrivileged {
		bullets = append(bullets, "postStartCommand uses docker compose, which needs nested Docker — NOT auto-enabled: container-privileged grants full host access. Re-run with --isolation container-privileged if you trust this repo.")
		return bullets
	}
	pr.archetypeDockerDRequired = true
	bullets = append(bullets, "dockerd will auto-start before lifecycle commands")
	return bullets
}

// applyDevcontainerEnv merges environment variables from devcontainer.json.
func applyDevcontainerEnv(dc *archetype.DevcontainerConfig, pr *profileResult, bullets []string) []string {
	merged := dc.MergedEnv()
	if len(merged) == 0 {
		return bullets
	}
	if pr.env == nil {
		pr.env = make(map[string]string)
	}
	for k, v := range merged {
		if _, exists := pr.env[k]; !exists {
			pr.env[k] = v
		}
	}
	return append(bullets, fmt.Sprintf("environment variables merged from devcontainer.json (%d keys)", len(merged)))
}

// applyDevcontainerPorts merges port forwards from devcontainer.json.
func applyDevcontainerPorts(dc *archetype.DevcontainerConfig, opts *Options, bullets []string) []string {
	ports := dc.ExtractPorts()
	if len(ports) == 0 {
		return bullets
	}
	seenPorts := make(map[string]bool)
	for _, p := range opts.Ports {
		seenPorts[p] = true
	}
	for _, p := range ports {
		if !seenPorts[p] {
			opts.Ports = append(opts.Ports, p)
			seenPorts[p] = true
		}
	}
	return append(bullets, fmt.Sprintf("ports %v forwarded", ports))
}

// applyDevcontainerWorkspaceFolder applies workspaceFolder to the workdir mount path.
func applyDevcontainerWorkspaceFolder(dc *archetype.DevcontainerConfig, opts *Options, bullets []string) []string {
	if dc.WorkspaceFolder == "" {
		return bullets
	}
	opts.Workdir.MountPath = dc.WorkspaceFolder
	return append(bullets, fmt.Sprintf("workdir mount path set to %s (workspaceFolder)", dc.WorkspaceFolder))
}

// appendLifecycleBullets adds lifecycle command summary bullets.
func appendLifecycleBullets(dc *archetype.DevcontainerConfig, bullets []string) []string {
	if !dc.OnCreateCommand.IsZero() {
		bullets = append(bullets, "onCreateCommand will run once at first start")
	}
	if !dc.UpdateContentCommand.IsZero() {
		bullets = append(bullets, "updateContentCommand will run once at first start")
	}
	if !dc.PostCreateCommand.IsZero() {
		bullets = append(bullets, "postCreateCommand will run once at first start")
	}
	if !dc.PostStartCommand.IsZero() {
		bullets = append(bullets, "postStartCommand will run on each start")
	}
	return bullets
}

// reportArchetype emits the transparency report for the resolved archetype:
// what was detected, what that selected, and what it changed.
//
// It is one notice rather than one per line. The lines are a single fact with
// structure -- this archetype, from these signals, with these effects -- and a
// consumer that is not a terminal wants the parts, not a transcript it has to
// reassemble. So the parts are Fields and the rendered block is the Message,
// which keeps the terminal output byte-identical to the seven Fprintfs this
// replaces while giving anything else the values directly.
func reportArchetype(sink feedback.Sink, arch archetype.Archetype, source string, signals []string, bullets []string) {
	if arch == archetype.ArchetypeSimple && source == "auto-detected" {
		return
	}
	var b strings.Builder
	switch {
	case len(signals) > 0:
		for _, sig := range signals {
			fmt.Fprintf(&b, "→ Detected %s\n", sig)
		}
	case source == "--archetype flag":
		fmt.Fprintf(&b, "→ --archetype %s\n", string(arch))
	}
	if arch != archetype.ArchetypeSimple {
		fmt.Fprintf(&b, "  Archetype: %s\n", string(arch))
		if len(bullets) > 0 {
			fmt.Fprint(&b, "  Because of this:\n")
			for _, bullet := range bullets {
				fmt.Fprintf(&b, "    · %s\n", bullet)
			}
		}
		fmt.Fprint(&b, "  To suppress: --archetype simple\n")
	}
	if b.Len() == 0 {
		return
	}
	feedback.Emit(sink, feedback.Notice{
		Event:   "archetype.resolved",
		Level:   feedback.LevelInfo,
		Message: strings.TrimSuffix(b.String(), "\n"),
		Fields: map[string]any{
			"archetype": string(arch),
			"source":    source,
			"signals":   signals,
			"effects":   bullets,
		},
	})
}

// validateAndExpandMounts validates and expands config mount paths.
// homeDir is used to expand leading "~" in host paths.
// env is the curated interpolation map for ${VAR} expansion.
func validateAndExpandMounts(mounts []string, homeDir string, env map[string]string) ([]string, error) {
	result := make([]string, len(mounts))
	for i, m := range mounts {
		spec, err := mountspkg.ParseConfigMount(m, homeDir, env)
		if err != nil {
			return nil, fmt.Errorf("invalid mount %q: %w", m, err)
		}
		result[i] = spec.HostPath + ":" + spec.ContainerPath
		if spec.ReadOnly {
			result[i] += ":ro"
		}
	}
	return result, nil
}
