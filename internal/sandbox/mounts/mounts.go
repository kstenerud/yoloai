// ABOUTME: Builds the bind-mount specs for a sandbox instance from resolved
// ABOUTME: State: workdir, aux dirs, agent runtime, home-seed, system dirs, git
// ABOUTME: identity, tmux config, and config/secrets mounts. Free functions
// ABOUTME: taking state.State so create/ and lifecycle/ can share them without
// ABOUTME: importing the sandbox façade.
package mounts

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// Build constructs the bind mounts for the sandbox instance.
func Build(st *state.State, secretsDir string) []runtime.MountSpec {
	var mounts []runtime.MountSpec
	mounts = append(mounts, buildWorkdirMounts(st)...)
	mounts = append(mounts, buildAuxDirMounts(st)...)
	mounts = append(mounts, buildAgentMounts(st)...)
	mounts = append(mounts, buildSystemMounts(st)...)
	mounts = append(mounts, buildGitAndTmuxMounts(st)...)
	mounts = append(mounts, buildConfigAndSecretsMounts(st, secretsDir)...)
	return mounts
}

// buildWorkdirMounts returns the mount specs for the sandbox workdir.
func buildWorkdirMounts(st *state.State) []runtime.MountSpec {
	switch st.Workdir.Mode {
	case "copy":
		return []runtime.MountSpec{{
			HostPath:      st.WorkCopyDir,
			ContainerPath: st.Workdir.ResolvedMountPath(),
		}}
	case "overlay":
		encoded := store.EncodePath(st.Workdir.Path)
		// Mount the entire overlay work base dir (upper/ovlwork/merged/lower) as
		// a single bind mount so upper and ovlwork share the same underlying Docker
		// volume — a kernel requirement for overlayfs to work inside a container.
		// The user's workdir is then nested on top as a read-only bind mount at
		// the lower/ subdirectory within the same volume.
		return []runtime.MountSpec{
			{
				HostPath:      store.OverlayWorkBaseDir(st.SandboxDir, st.Workdir.Path),
				ContainerPath: "/yoloai/overlay/" + encoded,
			},
			{
				HostPath:      st.Workdir.Path,
				ContainerPath: "/yoloai/overlay/" + encoded + "/lower",
				ReadOnly:      true,
			},
		}
	default:
		return []runtime.MountSpec{{
			HostPath:      st.Workdir.Path,
			ContainerPath: st.Workdir.ResolvedMountPath(),
			ReadOnly:      st.Workdir.Mode != "rw",
		}}
	}
}

// buildAuxDirMounts returns the mount specs for all auxiliary directories.
func buildAuxDirMounts(st *state.State) []runtime.MountSpec {
	var mounts []runtime.MountSpec
	for _, ad := range st.AuxDirs {
		mounts = append(mounts, buildSingleAuxDirMount(st.SandboxDir, ad)...)
	}
	return mounts
}

// buildSingleAuxDirMount returns mount specs for one auxiliary directory.
func buildSingleAuxDirMount(sandboxDir string, ad *state.DirSpec) []runtime.MountSpec {
	mountTarget := ad.ResolvedMountPath()
	switch ad.Mode {
	case "copy":
		return []runtime.MountSpec{{
			HostPath:      store.WorkDir(sandboxDir, ad.Path),
			ContainerPath: mountTarget,
		}}
	case "overlay":
		encoded := store.EncodePath(ad.Path)
		return []runtime.MountSpec{
			{
				HostPath:      store.OverlayWorkBaseDir(sandboxDir, ad.Path),
				ContainerPath: "/yoloai/overlay/" + encoded,
			},
			{
				HostPath:      ad.Path,
				ContainerPath: "/yoloai/overlay/" + encoded + "/lower",
				ReadOnly:      true,
			},
		}
	case "rw":
		return []runtime.MountSpec{{
			HostPath:      ad.Path,
			ContainerPath: mountTarget,
		}}
	default: // read-only (empty mode or explicit "ro")
		return []runtime.MountSpec{{
			HostPath:      ad.Path,
			ContainerPath: mountTarget,
			ReadOnly:      true,
		}}
	}
}

// buildAgentMounts returns mount specs for the agent runtime dir, VS Code CLI, and home-seed files.
func buildAgentMounts(st *state.State) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// Agent runtime directory (agent's own managed state)
	if st.Agent.StateDir != "" {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      filepath.Join(st.SandboxDir, store.AgentRuntimeDir),
			ContainerPath: st.Agent.StateDir,
		})
	}

	// VS Code CLI data dir
	if st.VscodeTunnel {
		mounts = append(mounts, buildVscodeMounts(st)...)
	}

	// Home-seed files and directories (mounted into /home/yoloai/)
	mounts = append(mounts, buildHomeSeedMounts(st)...)

	return mounts
}

// buildVscodeMounts returns mount specs for VS Code tunnel support.
func buildVscodeMounts(st *state.State) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// VS Code CLI data dir — per-sandbox to prevent singleton lock conflicts when
	// multiple sandboxes run tunnels concurrently. Token is seeded from the global
	// dir (~/.yoloai/vscode-cli/) on first use so re-authentication is only needed
	// once across all sandboxes.
	vscodeSandboxCLIDir := filepath.Join(st.SandboxDir, "vscode-cli")
	_ = fileutil.MkdirAll(vscodeSandboxCLIDir, 0750) //nolint:gosec // G301: sandbox dir, private

	// Seed token from global dir if this sandbox hasn't authenticated yet.
	globalTokenPath := filepath.Join(st.Layout.VscodeCLIDir(), "token.json")
	sandboxTokenPath := filepath.Join(vscodeSandboxCLIDir, "token.json")
	if _, err := os.Stat(sandboxTokenPath); os.IsNotExist(err) {
		if data, err2 := os.ReadFile(globalTokenPath); err2 == nil { //nolint:gosec // G304: path is sandbox-controlled
			_ = fileutil.WriteFile(sandboxTokenPath, data, 0600)
		}
	}

	mounts = append(mounts, runtime.MountSpec{
		HostPath:      vscodeSandboxCLIDir,
		ContainerPath: "/home/yoloai/.vscode/cli",
	})

	// Stable machine-id — VS Code CLI ties its token to /etc/machine-id; a
	// fresh random ID on each container restart causes re-authentication.
	machineIDPath := filepath.Join(st.SandboxDir, store.MachineIDFile)
	if err := ensureMachineID(machineIDPath); err == nil {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      machineIDPath,
			ContainerPath: "/etc/machine-id",
			ReadOnly:      true,
		})
	}

	return mounts
}

// buildHomeSeedMounts returns mount specs for home-seed files.
func buildHomeSeedMounts(st *state.State) []runtime.MountSpec {
	var mounts []runtime.MountSpec
	mountedDirs := map[string]bool{}
	for _, sf := range st.Agent.SeedFiles {
		if !sf.HomeDir {
			continue
		}
		// For nested paths (e.g., ".claude/settings.json"), mount the
		// top-level directory once rather than individual files. This lets
		// agents create new state files at runtime.
		if strings.Contains(sf.TargetPath, "/") {
			topDir, _, _ := strings.Cut(sf.TargetPath, "/")
			if mountedDirs[topDir] {
				continue
			}
			src := filepath.Join(st.SandboxDir, "home-seed", topDir)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      src,
				ContainerPath: "/home/yoloai/" + topDir,
			})
			mountedDirs[topDir] = true
		} else {
			src := filepath.Join(st.SandboxDir, "home-seed", sf.TargetPath)
			if _, err := os.Stat(src); err != nil {
				continue // skip if not seeded
			}
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      src,
				ContainerPath: "/home/yoloai/" + sf.TargetPath,
			})
		}
	}
	return mounts
}

// buildSystemMounts returns mount specs for logs, status, prompt, config, files, and cache.
func buildSystemMounts(st *state.State) []runtime.MountSpec {
	mounts := []runtime.MountSpec{
		// Structured log directory
		{
			HostPath:      filepath.Join(st.SandboxDir, store.LogsDir),
			ContainerPath: "/yoloai/" + store.LogsDir,
		},
		// Agent status file (for in-container status monitor)
		{
			HostPath:      filepath.Join(st.SandboxDir, store.AgentStatusFile),
			ContainerPath: "/yoloai/" + store.AgentStatusFile,
		},
	}

	// Prompt file
	if st.HasPrompt {
		promptSource := filepath.Join(st.SandboxDir, "prompt.txt")
		if st.PromptSourcePath != "" {
			promptSource = st.PromptSourcePath
		}
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      promptSource,
			ContainerPath: "/yoloai/prompt.txt",
			ReadOnly:      true,
		})
	}

	mounts = append(mounts,
		// Runtime config file
		runtime.MountSpec{
			HostPath:      filepath.Join(st.SandboxDir, store.RuntimeConfigFile),
			ContainerPath: "/yoloai/" + store.RuntimeConfigFile,
			ReadOnly:      true,
		},
		// File exchange directory
		runtime.MountSpec{
			HostPath:      filepath.Join(st.SandboxDir, "files"),
			ContainerPath: "/yoloai/files",
		},
		// Cache directory
		runtime.MountSpec{
			HostPath:      filepath.Join(st.SandboxDir, "cache"),
			ContainerPath: "/yoloai/cache",
		},
	)

	return mounts
}

// buildGitAndTmuxMounts returns mount specs for git identity and tmux configuration.
func buildGitAndTmuxMounts(st *state.State) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// Defaults tmux config
	if st.TmuxConf == "default" || st.TmuxConf == "default+host" {
		defaultsTmuxConf := filepath.Join(st.Layout.DefaultsDir(), "tmux.conf")
		if _, err := os.Stat(defaultsTmuxConf); err == nil {
			// Ensure the file is world-readable (0644). It may have been written
			// with 0600 by older yoloai versions. Inside Kata VMs the file is
			// mounted via virtiofs retaining its host uid, but the yoloai user
			// inside the VM (uid 1001) differs from the host user's uid, so
			// a 0600 file causes tmux to fail reading its config and enter
			// copy-mode — preventing send-keys from reaching the shell.
			_ = os.Chmod(defaultsTmuxConf, 0644) //nolint:gosec // G302: tmux.conf contains no secrets
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      defaultsTmuxConf,
				ContainerPath: "/yoloai/tmux/tmux.conf",
				ReadOnly:      true,
			})
		}
	}

	// Host tmux config (when tmux_conf is default+host or host)
	if st.TmuxConf == "default+host" || st.TmuxConf == "host" {
		tmuxConfPath := config.ExpandTilde("~/.tmux.conf", st.HomeDir)
		if _, err := os.Stat(tmuxConfPath); err == nil {
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      tmuxConfPath,
				ContainerPath: "/home/yoloai/.tmux.conf",
				ReadOnly:      true,
			})
		}
	}

	// Git identity: mount ~/.gitconfig and ~/.config/git/ read-only so that
	// git commands inside the container can resolve user.name / user.email.
	// Mirrors the symlink-based approach used by the Seatbelt backend.
	gitconfigPath := config.ExpandTilde("~/.gitconfig", st.HomeDir)
	if _, err := os.Stat(gitconfigPath); err == nil {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      gitconfigPath,
			ContainerPath: "/home/yoloai/.gitconfig",
			ReadOnly:      true,
		})
	}
	gitConfigDir := config.ExpandTilde("~/.config/git", st.HomeDir)
	if info, err := os.Stat(gitConfigDir); err == nil && info.IsDir() {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      gitConfigDir,
			ContainerPath: "/home/yoloai/.config/git",
			ReadOnly:      true,
		})
	}

	return mounts
}

// buildConfigAndSecretsMounts returns mount specs for config/profile mounts and secrets.
func buildConfigAndSecretsMounts(st *state.State, secretsDir string) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// Config/profile mounts (host:container[:ro])
	for _, m := range st.ConfigMounts {
		spec, err := ParseConfigMount(m, st.HomeDir, st.Layout.Env().EnvForConfigInterpolation())
		if err != nil {
			continue // skip unparseable mounts (validated at creation time)
		}
		mounts = append(mounts, spec)
	}

	// Secrets (env vars + API keys): mount the whole directory so that
	// Podman and Docker both work. Podman fails with per-file bind mounts
	// because its Docker-compatible API tries to mkdir the source path.
	// The entrypoint already iterates /run/secrets as a directory.
	if secretsDir != "" {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      secretsDir,
			ContainerPath: "/run/secrets",
			ReadOnly:      true,
		})
	}

	return mounts
}

// HasOverlayDirs returns true if the sandbox's workdir uses overlay
// mode. Q-U (2026-05-25) collapsed aux :overlay to the workdir only,
// so this is now a single-field check. Kept as a named predicate for
// callsite readability.
func HasOverlayDirs(st *state.State) bool {
	return st.Workdir.Mode == "overlay"
}

// ParseConfigMount parses a "host:container[:ro]" mount string into a MountSpec.
// The host path is expanded (tilde and ${VAR}).
// homeDir is used to expand leading "~" in the host path.
// env is the curated interpolation map for ${VAR} expansion.
func ParseConfigMount(s, homeDir string, env map[string]string) (runtime.MountSpec, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return runtime.MountSpec{}, fmt.Errorf("expected host:container[:ro] format")
	}

	hostPath, err := config.ExpandPath(parts[0], homeDir, env)
	if err != nil {
		return runtime.MountSpec{}, fmt.Errorf("expand host path: %w", err)
	}

	spec := runtime.MountSpec{
		HostPath:      hostPath,
		ContainerPath: parts[1],
	}

	if len(parts) == 3 {
		if parts[2] == "ro" {
			spec.ReadOnly = true
		} else {
			return runtime.MountSpec{}, fmt.Errorf("unknown mount option %q (expected \"ro\")", parts[2])
		}
	}

	return spec, nil
}

// ensureMachineID creates a stable machine-id file at path if it doesn't exist.
// The ID is a random 32-character lowercase hex string (same format as Linux
// /etc/machine-id) followed by a newline. It is bind-mounted read-only at
// /etc/machine-id in the container so that VS Code CLI sees a consistent machine
// fingerprint across container restarts and does not invalidate stored tokens.
func ensureMachineID(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	if err := fileutil.WriteFile(path, []byte(hex.EncodeToString(b)+"\n"), 0444); err != nil { //nolint:gosec // G703: path is a trusted sandbox subpath
		return err
	}
	return os.Chmod(path, 0444) //nolint:gosec // G302: machine-id is non-secret, world-readable by design
}
