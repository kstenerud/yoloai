// ABOUTME: HostEnv — the opaque, purpose-method curation of the host-env
// ABOUTME: snapshot. Env access is by named purpose; keysets are decided here,
// ABOUTME: centrally, not inline at each call site (§12).

package config

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// HostEnv is the curation boundary over the Layout's captured host-environment
// snapshot. Consuming code never enumerates env keys inline: it names a purpose
// (EnvForGitInvocation, EnvForDockerExec, …) and HostEnv hands back exactly the
// keyset that purpose needs. The keyset for each purpose is decided here —
// centrally and reviewably, the way GitEnv always was — so the decision of what
// a subprocess may see is made outside the code that spawns it (§12).
//
// HostEnv is a value carrying the snapshot plus the homeDir it needs to compute
// overrides (HOME, which under sudo differs from the snapshot's HOME; TART_HOME).
// Build it via Layout.Env(); never construct the snapshot anywhere but the edges
// (see Layout.WithEnv).
type HostEnv struct {
	vars    map[string]string
	homeDir string
}

// Env returns the curated view of this Layout's host-env snapshot. Cheap to call
// (it wraps the existing map and HomeDir); make one per use rather than caching.
func (l Layout) Env() HostEnv {
	return HostEnv{vars: l.env, homeDir: l.HomeDir}
}

// WithEnv returns a copy of the Layout carrying env as its host-env snapshot. It
// is the only way to populate the (unexported) snapshot, so capture stays at the
// edges: the CLI's licensed os.Environ read, yoloai.NewClient bridging the public
// ClientCreateOptions.Env, and tests. Library code receives a populated Layout
// and reads it only through the curated HostEnv accessors (layout.Env()).
func (l Layout) WithEnv(env map[string]string) Layout {
	l.env = env
	return l
}

// --- Centralized per-purpose allowlists ---------------------------------------
//
// These are the single source of truth for what each purpose may carry. They
// live in config (not in the backend packages) so the curation decision is made
// once, centrally, and is reviewable in one place. Backends name a purpose; they
// no longer declare their own allowlists inline.

// dockerExecAllowlist: docker/podman CLI subprocesses. PATH resolves the binary;
// the DOCKER_*/CONTAINER_HOST vars carry daemon connection settings; HOME is
// overridden (not allowlisted) so credential helpers can't read the ambient home;
// SSL vars carry TLS trust anchors for HTTPS pulls.
var dockerExecAllowlist = []string{
	"PATH", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR",
	"DOCKER_HOST", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION",
	"CONTAINER_HOST", "XDG_RUNTIME_DIR",
}

// dockerBuildAllowlist: docker/podman build subprocess — daemon connection,
// registry/credential-helper config, proxy settings for base-image pulls,
// SSH-agent forwarding, and rootless/buildx XDG locations. EnvForDockerBuild
// always forces DOCKER_BUILDKIT=1 on top.
var dockerBuildAllowlist = []string{
	"HOME", "PATH",
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG",
	"DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION",
	"CONTAINER_HOST", "CONTAINERS_CONF", "REGISTRY_AUTH_FILE",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "FTP_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "ftp_proxy", "all_proxy",
	"SSH_AUTH_SOCK",
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
	"BUILDX_CONFIG", "BUILDX_BUILDER",
}

// containerdExecAllowlist: containerd-adjacent CLI subprocesses. PATH resolves
// the binary; XDG_RUNTIME_DIR locates the rootless containerd socket; SSL vars
// carry TLS trust anchors. HOME is overridden, not allowlisted.
var containerdExecAllowlist = []string{
	"PATH", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR",
	"XDG_RUNTIME_DIR",
}

// tartEnvAllowlist: the tart CLI and the tools it spawns. HOME and TART_HOME are
// overridden from layout (not allowlisted) — TART_HOME is load-bearing because
// tart resolves its store via NSHomeDirectory(), so overriding $HOME alone does
// not redirect ~/.tart (DF19).
var tartEnvAllowlist = []string{"PATH", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"}

// appleExecAllowlist: the Apple `container` CLI. PATH locates the binary plus the
// plugins/helpers it execs under the install root; HOME backs the default
// CONTAINER_APP_ROOT (state root). Unlike docker/containerd, HOME is allowlisted
// (passthrough) rather than overridden: the `container` apiserver is a shared
// per-user launchd agent that resolves its state root from the real HOME, so
// pointing the CLI at a layout HOME would desync the CLI from the daemon without
// isolating anything (the daemon already runs under the real env). The CONTAINER_*
// roots let a user relocate state/install/logs; CONTAINER_DEFAULT_PLATFORM is
// honored below our explicit --os/--arch; CONTAINER_REGISTRY_* carry private-pull
// auth; CONTAINER_DEBUG toggles debug logging. SSH_AUTH_SOCK is deliberately
// excluded — yoloAI does not forward the host SSH agent into a sandbox.
var appleExecAllowlist = []string{
	"PATH", "HOME", "TMPDIR",
	"CONTAINER_APP_ROOT", "CONTAINER_INSTALL_ROOT", "CONTAINER_LOG_ROOT",
	"CONTAINER_DEFAULT_PLATFORM",
	"CONTAINER_REGISTRY_HOST", "CONTAINER_REGISTRY_USER", "CONTAINER_REGISTRY_TOKEN",
	"CONTAINER_DEBUG",
}

// seatbeltSandboxAllowlist: safe OS/locale vars passed into the seatbelt sandbox
// (and the seatbelt host subprocesses — sandbox-exec, tmux). Credentials
// (SSH_AUTH_SOCK, AWS_SECRET_ACCESS_KEY, …) are excluded; the entrypoint injects
// agent API keys from the secrets directory.
var seatbeltSandboxAllowlist = []string{
	"PATH", "HOME", "USER", "LOGNAME",
	"SHELL", "TERM", "TMPDIR",
	"LANG", "LC_ALL", "LC_CTYPE",
	"LC_COLLATE", "LC_MESSAGES", "LC_MONETARY",
	"LC_NUMERIC", "LC_TIME",
}

// daemonEnvAllowlist: keys container-backend probes and clients consult for
// daemon-socket discovery — the union of what the Docker SDK config reads
// (context/TLS/host) and what Podman socket discovery reads. TMPDIR is included
// because `podman machine inspect` on macOS derives the machine API socket path
// from it ($TMPDIR/podman/...); dropping it makes podman report the /tmp
// fallback path, which doesn't exist, so discovery fails. A superset is safe:
// each backend reads only its own keys. Mirrors the public runtime.DaemonEnvVars
// (which external callers of the public SelectBackend pass): config cannot import
// runtime (cycle), so the list is duplicated here; keep the two in sync.
var daemonEnvAllowlist = []string{
	"DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT",
	"DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION",
	"CONTAINER_HOST", "XDG_RUNTIME_DIR", "HOME", "TMPDIR",
}

// hostToolAllowlist: the minimal set for yoloAI's own host-side utility
// subprocesses (tmux, vscode, file copies, rsync, uname). PATH resolves the
// binary; HOME and TMPDIR keep them operating under the user's locations.
var hostToolAllowlist = []string{"PATH", "HOME", "TMPDIR"}

// diagnosticEnvAllowlist: the host-networking and yoloai-context vars a bug
// report captures — enough to explain most backend-connectivity issues, nothing
// sensitive.
var diagnosticEnvAllowlist = []string{
	"DOCKER_HOST", "CONTAINER_HOST", "XDG_RUNTIME_DIR", "YOLOAI_SANDBOX", "HOME", "TMUX",
}

// interpolationExactAllowlist + interpolationPrefixAllowlist: the only vars a
// config/profile ${VAR} reference may resolve. Deliberately tiny — this is what
// closes arbitrary config interpolation (${SECRET_KEY} no longer resolves). LC_*
// is a prefix match (covers LC_ALL/LC_CTYPE/…).
var interpolationExactAllowlist = []string{"HOME", "USER", "LANG", "TZ"}
var interpolationPrefixAllowlist = []string{"LC_"}

// --- Family A: yoloAI's own host-side subprocess envs ([]string) --------------

// EnvForGitInvocation is the curated environment for host-side git subprocesses
// (PATH/HOME/TMPDIR/SUDO_UID). See sysexec.GitEnv for the SUDO_UID rationale.
func (h HostEnv) EnvForGitInvocation() []string {
	return sysexec.GitEnv(h.vars)
}

// EnvForDockerExec is the environment for docker/podman CLI subprocesses. HOME is
// forced to the layout home so credential helpers can't read the ambient home.
func (h HostEnv) EnvForDockerExec() []string {
	return sysexec.Curated(h.vars, dockerExecAllowlist, map[string]string{"HOME": h.homeDir})
}

// EnvForDockerBuild is the environment for a docker/podman build subprocess. It
// carries only the allowlisted non-empty keys and always forces BuildKit on. An
// empty snapshot yields just DOCKER_BUILDKIT=1 — fail-closed: the build runs with
// no inherited host config rather than silently inheriting the process env (§12).
func (h HostEnv) EnvForDockerBuild() []string {
	env := make([]string, 0, len(dockerBuildAllowlist)+1)
	for _, key := range dockerBuildAllowlist {
		if v, ok := h.vars[key]; ok && v != "" {
			env = append(env, key+"="+v)
		}
	}
	return append(env, "DOCKER_BUILDKIT=1")
}

// EnvForContainerdExec is the environment for containerd-adjacent CLI
// subprocesses. HOME is forced to the layout home.
func (h HostEnv) EnvForContainerdExec() []string {
	return sysexec.Curated(h.vars, containerdExecAllowlist, map[string]string{"HOME": h.homeDir})
}

// EnvForSeatbeltExec is the environment for seatbelt host subprocesses
// (sandbox-exec, tmux).
func (h HostEnv) EnvForSeatbeltExec() []string {
	return sysexec.Curated(h.vars, seatbeltSandboxAllowlist, nil)
}

// EnvForSeatbeltSandbox is the environment injected into the seatbelt sandbox
// itself — safe OS/locale vars only; credentials arrive via the secrets dir.
func (h HostEnv) EnvForSeatbeltSandbox() []string {
	return sysexec.Curated(h.vars, seatbeltSandboxAllowlist, nil)
}

// EnvForTartInvocation is the environment for tart CLI invocations, with the
// HOME and TART_HOME overrides tart requires (DF19). An edge-resolved TART_HOME
// is honored; otherwise it defaults to <homeDir>/.tart.
func (h HostEnv) EnvForTartInvocation() []string {
	tartHome := filepath.Join(h.homeDir, ".tart")
	if v, ok := h.vars["TART_HOME"]; ok && v != "" {
		tartHome = v
	}
	return sysexec.Curated(h.vars, tartEnvAllowlist, map[string]string{
		"HOME":      h.homeDir,
		"TART_HOME": tartHome,
	})
}

// EnvForAppleContainer is the environment for Apple `container` CLI subprocesses.
// HOME is passed through (not overridden the way EnvForDockerExec overrides it):
// the container apiserver is a shared per-user launchd agent that resolves its
// state root from the real HOME, so a different HOME would desync the CLI from the
// daemon. See appleExecAllowlist.
func (h HostEnv) EnvForAppleContainer() []string {
	return sysexec.Curated(h.vars, appleExecAllowlist, nil)
}

// EnvForHostTool is the minimal environment for yoloAI's own host utility
// subprocesses (tmux, vscode, file copies, rsync, uname): PATH/HOME/TMPDIR.
func (h HostEnv) EnvForHostTool() []string {
	return sysexec.Curated(h.vars, hostToolAllowlist, nil)
}

// PassthroughEnv returns the entire snapshot as a sorted KEY=VALUE slice. It is
// the ONE sanctioned full-passthrough: `yoloai x` runs user-authored extension
// scripts via `sh -c`, and those scripts get the user's full edge-resolved
// environment by design. Library code that shells out must use a curated
// EnvFor… accessor instead, never this.
func (h HostEnv) PassthroughEnv() []string {
	out := make([]string, 0, len(h.vars))
	for k, v := range h.vars {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// --- Map-returning accessors --------------------------------------------------

// EnvForDaemonDiscovery returns the curated daemon-socket-discovery subset, for
// the Docker SDK client config and Podman socket discovery, which index daemon
// keys directly. Always non-nil.
func (h HostEnv) EnvForDaemonDiscovery() map[string]string {
	return curatedMap(h.vars, daemonEnvAllowlist)
}

// EnvForDiagnostics returns the curated bug-report subset (host-networking and
// yoloai-context vars). Always non-nil.
func (h HostEnv) EnvForDiagnostics() map[string]string {
	return curatedMap(h.vars, diagnosticEnvAllowlist)
}

// EnvForConfigInterpolation returns the fixed-allowlist map that config/profile
// ${VAR} expansion may resolve. This is the only env a ${VAR} reference ever
// sees — arbitrary vars (e.g. ${SECRET_KEY}) do not resolve. Always non-nil.
func (h HostEnv) EnvForConfigInterpolation() map[string]string {
	out := curatedMap(h.vars, interpolationExactAllowlist)
	for k, v := range h.vars {
		for _, prefix := range interpolationPrefixAllowlist {
			if strings.HasPrefix(k, prefix) {
				out[k] = v
				break
			}
		}
	}
	return out
}

// EnvForAgentCredentials returns the present (non-empty) subset of declaredKeys.
// It is the one accessor that takes an argument: credentials are inherently
// per-agent and declaredKeys is declared data from the agent definition
// (APIKeyEnvVars / AuthHintEnvVars), not an inline literal. Keeping the keys as a
// parameter keeps config agent-agnostic (no config→agent import). Always non-nil.
func (h HostEnv) EnvForAgentCredentials(declaredKeys []string) map[string]string {
	out := make(map[string]string, len(declaredKeys))
	for _, key := range declaredKeys {
		if v, ok := h.vars[key]; ok && v != "" {
			out[key] = v
		}
	}
	return out
}

// --- Honest non-subprocess accessors ------------------------------------------

// InHostTmux reports whether the process is running inside a tmux session (TMUX
// is set in the snapshot). Not a subprocess env — a plain query.
func (h HostEnv) InHostTmux() bool {
	return h.vars["TMUX"] != ""
}

// TerminalColumns reports the terminal width from COLUMNS, and whether it was
// present and parseable. Not a subprocess env — a plain query.
func (h HostEnv) TerminalColumns() (int, bool) {
	s, ok := h.vars["COLUMNS"]
	if !ok || s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// curatedMap returns the allowlisted subset of vars as a new (always non-nil)
// map. Only exact keys named in allow are carried.
func curatedMap(vars map[string]string, allow []string) map[string]string {
	out := make(map[string]string, len(allow))
	for _, k := range allow {
		if v, ok := vars[k]; ok {
			out[k] = v
		}
	}
	return out
}
