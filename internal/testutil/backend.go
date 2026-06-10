// ABOUTME: YOLOAI_TEST_BACKEND resolver and NewIntegrationRuntime constructor for
// ABOUTME: parametrizing integration tests across docker/podman/containerd backends.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	yrt "github.com/kstenerud/yoloai/internal/runtime"
)

// integrationBackendEnv selects the backend used by parametrized integration
// tests. Default is "docker"; CI's podman job sets it to "podman".
const integrationBackendEnv = "YOLOAI_TEST_BACKEND"

// IntegrationBackendType returns the backend name driven by
// YOLOAI_TEST_BACKEND, defaulting to "docker". Callers must ensure the
// backend has been registered (via blank-import of its runtime package).
func IntegrationBackendType() string {
	if name := os.Getenv(integrationBackendEnv); name != "" {
		return name
	}
	return "docker"
}

// GetCuratedHostEnv captures the allowlisted subset of the process environment
// as a map. It is THE single licensed test-edge read of the host environment —
// the test-side equivalent of the CLI's licensed os.Environ() boundary read —
// but it REQUIRES an allowlist: a caller obtains only the vars it names, never
// the whole ambient map. Curation happens at the read, so no test caller can
// grab the full environment and forward it uncurated (the mistake a bare
// snapshot getter invites). Each call site declares exactly what it needs,
// mirroring config.Layout's curated accessors. forbidigo-gated in
// .golangci.yml; new callers join the reviewed allowlist (DEV §12).
func GetCuratedHostEnv(allow []string) map[string]string {
	want := make(map[string]bool, len(allow))
	for _, k := range allow {
		want[k] = true
	}
	m := make(map[string]string, len(allow))
	for _, e := range os.Environ() { //nolint:forbidigo // §12: THE single licensed test-edge env read; allowlist-curated at the source
		if k, v, ok := strings.Cut(e, "="); ok && want[k] {
			m[k] = v
		}
	}
	return m
}

// IntegrationHostEnvVars is the host-env allowlist an integration-test backend
// legitimately reads: the union of git (PATH/HOME/TMPDIR/SUDO_UID), daemon
// discovery + TLS (DOCKER_*/CONTAINER_HOST/XDG_*), image-build config (registry/
// proxy/ssh-agent), the Tart store location, and OS/locale. It is the test-edge
// superset threaded into an integration Layout; the Layout's per-use curated
// accessors (ExecEnv/CuratedEnv/GitEnv) narrow it further for each subprocess.
// Defined once so the integration callers stay DRY and consistent (DEV §12).
var IntegrationHostEnvVars = []string{
	"PATH", "HOME", "TMPDIR", "SUDO_UID",
	"DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT",
	"DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION",
	"CONTAINER_HOST", "CONTAINERS_CONF", "REGISTRY_AUTH_FILE",
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "SSH_AUTH_SOCK",
	"TART_HOME", "BUILDX_CONFIG", "BUILDX_BUILDER",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "FTP_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "no_proxy", "ftp_proxy", "all_proxy",
	"USER", "LOGNAME", "SHELL", "TERM",
	"LANG", "LC_ALL", "LC_CTYPE", "LC_COLLATE",
	"LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME",
}

// NewIntegrationRuntime constructs the runtime named by YOLOAI_TEST_BACKEND
// (default "docker"). On failure it calls t.Fatal with the backend name so
// the source of the failure is unambiguous. The returned runtime must be
// closed by the caller.
func NewIntegrationRuntime(ctx context.Context, t *testing.T) yrt.Runtime {
	t.Helper()
	name := IntegrationBackendType()
	home, _ := os.UserHomeDir()
	// Tests are the boundary equivalent of the CLI's licensed os.Environ read:
	// thread the host env so backend socket discovery (e.g. podman's
	// XDG_RUNTIME_DIR) sees the real environment, not an empty map.
	layout := config.NewLayoutFor(filepath.Join(home, ".yoloai", "library"), home).WithEnv(GetCuratedHostEnv(IntegrationHostEnvVars))
	// Namespace this runtime to a unique principal so a prune sweep in an
	// integration test can only ever match yoloai-<principal>-*, never the
	// developer's real resources (DEV §12, DF19). Shares the one principal
	// source with the system Client tests.
	layout = layout.WithPrincipal(UniqueTestPrincipal(t))
	rt, err := yrt.New(ctx, yrt.BackendType(name), layout)
	if err != nil {
		t.Fatalf("create %q runtime: %v", name, err)
	}
	return rt
}
