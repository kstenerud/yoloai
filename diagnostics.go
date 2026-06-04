// ABOUTME: Public diagnostics surface — a structured host/install snapshot the
// ABOUTME: CLI's bug-report renders to markdown. Library gathers; policy renders.

package yoloai

import (
	"context"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// Diagnostics is a structured, point-in-time snapshot of the host and yoloai
// installation. It is the data product behind a bug report: the library gathers
// the facts (mechanism) and the caller renders them (policy) — no human-readable
// strings, markdown, or redaction happen here. Config blobs are returned RAW and
// unredacted; a caller producing a shareable report is responsible for
// sanitizing them (see development-principles.md §2).
type Diagnostics struct {
	System   SystemDiagnostics
	Backends []BackendDiagnostic
	// VMSlots is the host VM-slot census, or nil on platforms/backends without
	// one (e.g. Linux, or when tart can't be constructed).
	VMSlots *VMCensus
	Config  ConfigDiagnostics
}

// SystemDiagnostics captures host identity and yoloai's on-disk footprint.
type SystemDiagnostics struct {
	OS     string // goruntime.GOOS
	Arch   string // goruntime.GOARCH
	Kernel string // `uname -a`, empty if unavailable
	// Env holds the allowlisted diagnostic environment variables that were set,
	// in allowlist order. Only set variables appear.
	Env     []EnvVar
	DataDir string
	// DiskUsageBytes is the total size under DataDir, or -1 when it could not
	// be measured (e.g. the directory does not exist).
	DiskUsageBytes int64
}

// EnvVar is one captured environment variable.
type EnvVar struct {
	Key   string
	Value string
}

// BackendDiagnostic is one backend's availability and version, in registration
// order.
type BackendDiagnostic struct {
	Type      BackendType
	Available bool
	// Note carries the probe-failure reason when Available is false; "" otherwise.
	Note string
	// Version is the backend's reported version when available and reportable;
	// "" when the backend is unavailable or exposes no version hook.
	Version string
}

// ConfigDiagnostics holds the raw bytes of yoloai's two config files plus their
// paths. Raw and unredacted by design — redaction is the caller's policy.
type ConfigDiagnostics struct {
	GlobalPath  string
	GlobalRaw   []byte
	ProfilePath string
	ProfileRaw  []byte
}

// diagnosticEnvVars is the allowlist of environment variables a bug report
// captures — the host-networking and yoloai-context vars that explain most
// backend-connectivity issues, and nothing sensitive.
var diagnosticEnvVars = []string{"DOCKER_HOST", "CONTAINER_HOST", "XDG_RUNTIME_DIR", "YOLOAI_SANDBOX", "HOME", "TMUX"}

// Diagnostics gathers a structured host/install snapshot for bug reports. It
// probes every registered backend (constructing and closing each), reads the
// VM-slot census, and reads both config files raw. Best-effort throughout:
// unreadable pieces are reported as zero/empty values rather than failing.
func (s *System) Diagnostics(ctx context.Context) Diagnostics {
	return Diagnostics{
		System:   s.systemDiagnostics(),
		Backends: s.backendDiagnostics(ctx),
		VMSlots:  s.VMCensus(ctx),
		Config:   s.configDiagnostics(),
	}
}

func (s *System) systemDiagnostics() SystemDiagnostics {
	sys := SystemDiagnostics{
		OS:             goruntime.GOOS,
		Arch:           goruntime.GOARCH,
		DataDir:        s.layout.YoloaiDir(),
		DiskUsageBytes: -1,
	}

	if out, err := exec.Command("uname", "-a").Output(); err == nil {
		sys.Kernel = strings.TrimSpace(string(out))
	}

	for _, key := range diagnosticEnvVars {
		if val := s.layout.Env[key]; val != "" {
			sys.Env = append(sys.Env, EnvVar{Key: key, Value: val})
		}
	}

	if _, err := os.Stat(sys.DataDir); err == nil {
		sys.DiskUsageBytes = dirSize(sys.DataDir)
	}

	return sys
}

func (s *System) backendDiagnostics(ctx context.Context) []BackendDiagnostic {
	descs := runtime.Descriptors()
	out := make([]BackendDiagnostic, 0, len(descs))
	for _, desc := range descs {
		available, note := s.CheckBackend(ctx, desc.Type)
		bd := BackendDiagnostic{Type: desc.Type, Available: available, Note: note}
		if available && desc.VersionString != nil {
			bd.Version = desc.VersionString(ctx)
		}
		out = append(out, bd)
	}
	return out
}

func (s *System) configDiagnostics() ConfigDiagnostics {
	cfg := ConfigDiagnostics{
		GlobalPath:  s.layout.GlobalConfigPath(),
		ProfilePath: s.layout.DefaultsConfigPath(),
	}
	if data, err := config.ReadGlobalConfigRaw(s.layout); err == nil {
		cfg.GlobalRaw = data
	}
	if data, err := config.ReadConfigRaw(s.layout); err == nil {
		cfg.ProfileRaw = data
	}
	return cfg
}
