// ABOUTME: Archetype resolution and expansion pipeline — resolves the active
// ABOUTME: archetype (CLI, .yoloai.yaml, or auto-detect), validates platform
// ABOUTME: requirements, and expands archetype effects onto opts and profileResult.
package create

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/archetype"
	mountspkg "github.com/kstenerud/yoloai/internal/orchestrator/mounts"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// resolveAndApplyArchetype loads .yoloai.yaml, resolves the archetype with priority
// (CLI > .yoloai.yaml > auto-detect), validates platform requirements, handles
// requires: prompts, expands archetype effects on opts and pr, and prints transparency output.
//
// Returns: (resolved archetype, devcontainer config, safe devcontainer mounts, mount warnings, error).
func resolveAndApplyArchetype(ctx context.Context, d state.Deps, opts *Options, pr *profileResult) (archetype.Archetype, *archetype.DevcontainerConfig, []string, []string, error) {
	workdir := opts.Workdir.Path

	// Step 1: Load .yoloai.yaml
	yamlCfg, _, yamlErr := archetype.LoadYoloAIYaml(workdir, d.Layout.HomeDir, d.Layout.Env().EnvForConfigInterpolation())
	if yamlErr != nil {
		return "", nil, nil, nil, fmt.Errorf("load .yoloai.yaml: %w", yamlErr)
	}

	arch, signals, source, err := resolveArchetype(opts, yamlCfg, workdir)
	if err != nil {
		return "", nil, nil, nil, err
	}

	// Step 2: Platform check for apple archetype
	if err := checkAppleArchetype(outputFor(opts.Output), arch, opts.Archetype); err != nil {
		return "", nil, nil, nil, err
	}

	// Step 3: requires: validation (warning only — version verification unimplemented)
	checkRequires(outputFor(opts.Output), yamlCfg)

	// Step 4: Archetype expansion
	devcontainerCfg, dcMounts, dcMountWarnings, bullets, err := expandArchetype(ctx, d, opts, pr, arch, yamlCfg)
	if err != nil {
		return "", nil, nil, nil, err
	}

	// Step 5: Transparency output
	printArchetypeOutput(outputFor(opts.Output), arch, source, signals, bullets)

	return arch, devcontainerCfg, dcMounts, dcMountWarnings, nil
}

// resolveArchetype determines the archetype from CLI, .yoloai.yaml, or auto-detection.
func resolveArchetype(opts *Options, yamlCfg *archetype.YoloAIProjectConfig, workdir string) (archetype.Archetype, []string, string, error) {
	switch {
	case opts.Archetype != "":
		a, err := archetype.ParseArchetype(opts.Archetype)
		if err != nil {
			return "", nil, "", err
		}
		return a, nil, "--archetype flag", nil
	case yamlCfg != nil && yamlCfg.Archetype != "":
		a, err := archetype.ParseArchetype(yamlCfg.Archetype)
		if err != nil {
			return "", nil, "", err
		}
		return a, nil, ".yoloai.yaml", nil
	default:
		arch, signals := archetype.DetectArchetype(workdir)
		return arch, signals, "auto-detected", nil
	}
}

// checkAppleArchetype validates platform requirements for the apple archetype.
func checkAppleArchetype(output io.Writer, arch archetype.Archetype, cliArchetype string) error {
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
	fmt.Fprintf(output, "Warning: This looks like an Apple platform project. The Tart backend requires Apple Silicon macOS.\n") //nolint:errcheck // best-effort warning
	return nil
}

// checkRequires warns about the requires: constraints from .yoloai.yaml.
// Version verification is not yet implemented, so this is a non-blocking
// notice — there is nothing to enforce, so proceeding is always correct. When
// real verification lands it should refuse with a typed *RequirementsNotMetError
// carrying the offending tool/version, not gate on "unverified".
func checkRequires(output io.Writer, yamlCfg *archetype.YoloAIProjectConfig) {
	if yamlCfg == nil || len(yamlCfg.Requires) == 0 {
		return
	}
	for tool, constraint := range yamlCfg.Requires {
		fmt.Fprintf(output, "Warning: requires: %s %s — version verification not yet implemented; continuing.\n", tool, constraint) //nolint:errcheck // best-effort warning
	}
}

// expandArchetype applies archetype-specific settings to opts and pr.
// Returns (devcontainerCfg, dcMounts, dcMountWarnings, bullets, error).
func expandArchetype(ctx context.Context, d state.Deps, opts *Options, pr *profileResult, arch archetype.Archetype, yamlCfg *archetype.YoloAIProjectConfig) (*archetype.DevcontainerConfig, []string, []string, []string, error) {
	var bullets []string
	var devcontainerCfg *archetype.DevcontainerConfig
	var dcMounts []string
	var dcMountWarnings []string

	switch arch {
	case archetype.ArchetypeCompose:
		bullets = applyComposeArchetype(opts, pr)
	case archetype.ArchetypeDevcontainer:
		var err error
		devcontainerCfg, dcMounts, dcMountWarnings, bullets, err = applyDevcontainerArchetype(ctx, d, opts, pr)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	case archetype.ArchetypeApple:
		bullets = append(bullets, "backend=tart required (Apple Silicon macOS VM)")
	case archetype.ArchetypeSimple:
		// no-op
	}

	mergeYamlMounts(pr, yamlCfg)
	return devcontainerCfg, dcMounts, dcMountWarnings, bullets, nil
}

// applyComposeArchetype applies compose-specific settings to opts and pr.
func applyComposeArchetype(opts *Options, pr *profileResult) []string {
	var bullets []string
	if opts.Isolation == "" || opts.Isolation == runtime.IsolationModeContainer {
		opts.Isolation = runtime.IsolationModeContainerPrivileged
		pr.isolation = runtime.IsolationModeContainerPrivileged
		bullets = append(bullets, "isolation set to container-privileged (Compose requires nested Docker)")
	}
	pr.archetypeDockerDRequired = true
	bullets = append(bullets, "dockerd will auto-start before lifecycle commands")
	return bullets
}

// applyDevcontainerArchetype loads and applies devcontainer.json settings.
func applyDevcontainerArchetype(ctx context.Context, d state.Deps, opts *Options, pr *profileResult) (*archetype.DevcontainerConfig, []string, []string, []string, error) {
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

	dc.WarnIgnoredFields(outputFor(opts.Output))

	bullets = applyDevcontainerRunArgs(dc, pr, bullets, outputFor(opts.Output))
	bullets = applyDevcontainerCompose(dc, opts, pr, bullets)
	bullets = applyDevcontainerEnv(dc, pr, bullets)
	bullets = applyDevcontainerPorts(dc, opts, bullets)
	bullets = applyDevcontainerWorkspaceFolder(dc, opts, bullets)

	workdirMountPath := opts.Workdir.MountPath
	if workdirMountPath == "" {
		workdirMountPath = opts.Workdir.Path
	}
	dcMounts, dcMountWarnings := dc.FilterMounts(workdirMountPath, d.Layout.HomeDir)
	if len(dcMounts) > 0 {
		bullets = append(bullets, fmt.Sprintf("%d devcontainer mounts passed through", len(dcMounts)))
	}

	bullets = appendLifecycleBullets(dc, bullets)

	return dc, dcMounts, dcMountWarnings, bullets, nil
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
func applyDevcontainerRunArgs(dc *archetype.DevcontainerConfig, pr *profileResult, bullets []string, output io.Writer) []string {
	cpus, memory, capAdd, unknownWarnings := dc.ParsedRunArgs()
	for _, w := range unknownWarnings {
		fmt.Fprintln(output, w) //nolint:errcheck // best-effort warning
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
	if opts.Isolation == "" || opts.Isolation == runtime.IsolationModeContainer {
		opts.Isolation = runtime.IsolationModeContainerPrivileged
		pr.isolation = runtime.IsolationModeContainerPrivileged
		bullets = append(bullets, "isolation set to container-privileged (postStartCommand uses docker compose)")
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

// mergeYamlMounts adds .yoloai.yaml mounts to pr.mounts (dedup).
func mergeYamlMounts(pr *profileResult, yamlCfg *archetype.YoloAIProjectConfig) {
	if yamlCfg == nil || len(yamlCfg.Mounts) == 0 {
		return
	}
	seenYamlMounts := make(map[string]bool)
	for _, mount := range pr.mounts {
		seenYamlMounts[mount] = true
	}
	for _, mount := range yamlCfg.Mounts {
		if !seenYamlMounts[mount] {
			pr.mounts = append(pr.mounts, mount)
			seenYamlMounts[mount] = true
		}
	}
}

// printArchetypeOutput prints transparency information about the resolved archetype.
func printArchetypeOutput(output io.Writer, arch archetype.Archetype, source string, signals []string, bullets []string) {
	if arch == archetype.ArchetypeSimple && source == "auto-detected" {
		return
	}
	switch {
	case len(signals) > 0:
		for _, sig := range signals {
			fmt.Fprintf(output, "→ Detected %s\n", sig) //nolint:errcheck // best-effort output
		}
	case source == ".yoloai.yaml":
		fmt.Fprintf(output, "→ .yoloai.yaml declares archetype: %s\n", string(arch)) //nolint:errcheck // best-effort output
	case source == "--archetype flag":
		fmt.Fprintf(output, "→ --archetype %s\n", string(arch)) //nolint:errcheck // best-effort output
	}
	if arch != archetype.ArchetypeSimple {
		fmt.Fprintf(output, "  Archetype: %s\n", string(arch)) //nolint:errcheck // best-effort output
		if len(bullets) > 0 {
			fmt.Fprintln(output, "  Because of this:") //nolint:errcheck // best-effort output
			for _, b := range bullets {
				fmt.Fprintf(output, "    · %s\n", b) //nolint:errcheck // best-effort output
			}
		}
		fmt.Fprintf(output, "  To suppress: --archetype simple\n") //nolint:errcheck // best-effort output
	}
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
