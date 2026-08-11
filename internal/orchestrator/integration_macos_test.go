//go:build integration

// ABOUTME: macOS-backend (apple, seatbelt) variants of the audit-C1 malicious-filter
// ABOUTME: regression — proving work-copy git now runs in-confinement, not on the host.

package orchestrator_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/copyflow"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime/apple"
	"github.com/kstenerud/yoloai/runtime/seatbelt"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_CopyModeMaliciousFilterNoHostExec_Seatbelt is the audit-C1
// containment check on seatbelt: work-copy git now runs under a dedicated tight
// sandbox-exec SBPL profile (GenerateGitProfile), so a planted filter.<x>.clean
// cannot touch a host marker outside the work copy. Gated behind
// YOLOAI_TEST_SEATBELT=1 (macOS + sandbox-exec required). The seatbelt keepalive
// need NOT be running — the confined GitExec wraps git itself — so this exercises
// create → plant → diff without booting the agent monitor.
func TestIntegration_CopyModeMaliciousFilterNoHostExec_Seatbelt(t *testing.T) {
	mgr, ctx := seatbeltMaliciousSetup(t)
	// timeout 0 → do not boot: the seatbelt confined GitExec wraps git under
	// sandbox-exec directly and needs no running keepalive.
	runMaliciousFilterAssertion(ctx, t, mgr, "evilfilter-seatbelt", 0)
}

// TestIntegration_CopyModeMaliciousFilterNoHostExec_Apple is the same C1 check on
// the apple backend, whose GitExec dispatches into the per-container VM. Gated
// behind YOLOAI_TEST_APPLE=1 (macOS 26 + Apple Silicon + a built yoloai-base).
// Apple's GitExec requires the container running, so this boots it first.
func TestIntegration_CopyModeMaliciousFilterNoHostExec_Apple(t *testing.T) {
	mgr, ctx := appleMaliciousSetup(t)
	// Apple's GitExec dispatches into the running container, so boot it first.
	runMaliciousFilterAssertion(ctx, t, mgr, "evilfilter-apple", 90*time.Second)
}

// TestIntegration_CustomDNS_Apple exercises configuration, persistence, Apple
// argv, guest resolver verification, isolated firewall ordering, and recreation
// through the real Apple backend. It intentionally fails rather than skips when
// the release host cannot reach either public resolver: that is this feature's
// live contract.
func TestIntegration_CustomDNS_Apple(t *testing.T) {
	mgr, ctx := appleMaliciousSetup(t)
	projectDir := createProjectDir(t)
	const name = "customdns-apple"
	const selectedResolver = "1.1.1.1"
	const unselectedResolver = "8.8.8.8"
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    name,
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
		DNS:     []string{selectedResolver},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = destroySandbox(ctx, mgr, name) })

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir(name))
	require.NoError(t, err)
	assert.Equal(t, []string{selectedResolver}, meta.DNS)
	startAndWaitActive(ctx, t, mgr, name)
	instance := store.InstanceName(mgr.Layout().Principal, name)
	result, err := mgr.Runtime().Exec(ctx, instance, []string{"cat", "/etc/resolv.conf"}, "yoloai")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "nameserver "+selectedResolver)
	_, err = mgr.Runtime().Exec(ctx, instance, []string{"getent", "hosts", "example.com"}, "yoloai")
	require.NoError(t, err, "custom DNS must resolve a public hostname in open mode")

	const isolatedName = "customdns-isolated-apple"
	_, err = createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:         isolatedName,
		Workdir:      orchestrator.DirSpec{Path: projectDir},
		Agent:        "test",
		Version:      "test",
		DNS:          []string{selectedResolver},
		Network:      orchestrator.NetworkModeIsolated,
		NetworkAllow: []string{"dns.google"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = destroySandbox(ctx, mgr, isolatedName) })

	// Prove the unselected resolver answers from the host before treating the
	// isolated guest timeout as firewall denial.
	hostProbe := sysexec.CommandContext(ctx, mgr.Layout().Env().EnvForHostTool(), "dig", "+time=3", "+tries=1", "@"+unselectedResolver, "example.com")
	require.NoError(t, hostProbe.Run(), "control resolver must answer on the release host")
	startAndWaitActive(ctx, t, mgr, isolatedName)
	isolatedInstance := store.InstanceName(mgr.Layout().Principal, isolatedName)
	_, err = mgr.Runtime().Exec(ctx, isolatedInstance, []string{"getent", "hosts", "example.com"}, "yoloai")
	require.NoError(t, err, "isolated custom DNS must resolve a public hostname")
	_, err = mgr.Runtime().Exec(ctx, isolatedInstance, []string{"dig", "+notcp", "@" + selectedResolver, "example.com"}, "yoloai")
	require.NoError(t, err, "isolated custom DNS must permit UDP port 53")
	_, err = mgr.Runtime().Exec(ctx, isolatedInstance, []string{"dig", "+tcp", "@" + selectedResolver, "example.com"}, "yoloai")
	require.NoError(t, err, "isolated custom DNS must permit TCP port 53")
	rules, err := mgr.Runtime().Exec(ctx, isolatedInstance, []string{"sudo", "-n", "iptables", "-S", "OUTPUT"}, "yoloai")
	require.NoError(t, err)
	udpAccept := "-A OUTPUT -d " + selectedResolver + " -p udp --dport 53 -j ACCEPT"
	tcpAccept := "-A OUTPUT -d " + selectedResolver + " -p tcp --dport 53 -j ACCEPT"
	udpReject := "-A OUTPUT -p udp --dport 53 -j REJECT"
	tcpReject := "-A OUTPUT -p tcp --dport 53 -j REJECT"
	allow := "-A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT"
	udpAcceptAt := canonicalRuleIndex(t, rules.Stdout, udpAccept)
	tcpAcceptAt := canonicalRuleIndex(t, rules.Stdout, tcpAccept)
	udpRejectAt := canonicalRuleIndex(t, rules.Stdout, udpReject)
	tcpRejectAt := canonicalRuleIndex(t, rules.Stdout, tcpReject)
	allowAt := canonicalRuleIndex(t, rules.Stdout, allow)
	assert.Less(t, udpAcceptAt, udpRejectAt, "UDP accept must precede its global reject")
	assert.Less(t, tcpAcceptAt, tcpRejectAt, "TCP accept must precede its global reject")
	assert.Less(t, udpRejectAt, allowAt, "UDP reject must precede broad allowlist acceptance")
	assert.Less(t, tcpRejectAt, allowAt, "TCP reject must precede broad allowlist acceptance")
	_, err = mgr.Runtime().Exec(ctx, isolatedInstance, []string{"dig", "+time=1", "+tries=1", "@" + unselectedResolver, "example.com"}, "yoloai")
	require.Error(t, err, "an allowlisted but unselected answering resolver must be denied")
	hostHTTPS := sysexec.CommandContext(ctx, mgr.Layout().Env().EnvForHostTool(), "curl", "--fail", "--silent", "--show-error", "--connect-timeout", "3", "https://example.com")
	require.NoError(t, hostHTTPS.Run(), "control TCP endpoint must be reachable from the release host")
	_, err = mgr.Runtime().Exec(ctx, isolatedInstance, []string{"curl", "--fail", "--silent", "--show-error", "--connect-timeout", "3", "https://example.com"}, "yoloai")
	require.Error(t, err, "the isolated guest must not gain unrelated TCP egress")

	// Changing defaults must not affect a captured snapshot when the native
	// instance is missing and lifecycle recreates it.
	require.NoError(t, os.WriteFile(mgr.Layout().DefaultsConfigPath(), []byte("network:\n  dns: [8.8.8.8]\n"), 0600))
	require.NoError(t, mgr.Runtime().Remove(ctx, instance))
	startAndWaitActive(ctx, t, mgr, name)
	result, err = mgr.Runtime().Exec(ctx, instance, []string{"cat", "/etc/resolv.conf"}, "yoloai")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "nameserver "+selectedResolver)
	assert.NotContains(t, result.Stdout, "nameserver "+unselectedResolver)
}

// canonicalRuleIndex compares iptables' canonical rule spelling. iptables -S
// expands protocol matches ("-m udp"/"-m tcp") and host destinations (/32),
// neither of which changes the rule we installed.
func canonicalRuleIndex(t *testing.T, output, expected string) int {
	t.Helper()
	want := canonicalRuleTokens(strings.Fields(expected))
	for index, line := range strings.Split(output, "\n") {
		if strings.Join(canonicalRuleTokens(strings.Fields(line)), " ") == strings.Join(want, " ") {
			return index
		}
	}
	require.Failf(t, "missing iptables rule", "expected %q in:\n%s", expected, output)
	return -1
}

func canonicalRuleTokens(tokens []string) []string {
	canonical := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		if tokens[i] == "-m" && i+1 < len(tokens) && (tokens[i+1] == "udp" || tokens[i+1] == "tcp") {
			i++
			continue
		}
		canonical = append(canonical, strings.TrimSuffix(tokens[i], "/32"))
	}
	return canonical
}

// seatbeltMaliciousSetup builds an Engine on a real seatbelt backend with an
// isolated HOME. The layout carries both the DataDir (for the on-host work copy)
// and a curated host env whose HOME is forced to the isolated home, so the git
// SBPL profile (which allows <home>/.gitconfig) and the git invocation agree on
// which HOME git reads — a mismatch would make git fail on the real ~/.gitconfig.
func seatbeltMaliciousSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	if goruntime.GOOS != "darwin" {
		t.Skip("seatbelt is macOS-only")
	}
	// No cost gate: this test takes ~0.5s and needs no daemon. It used to sit
	// behind YOLOAI_TEST_SEATBELT=1, which nothing set — not the Makefile, not
	// CI, not any script — so the C1 containment check it performs had never run
	// once, on the backend that needs it most (seatbelt has no container; the
	// confinement is an SBPL profile wrapping git itself). A gate with no cost
	// to justify it is a deleted test that reports green (DF99, DF95).
	ctx := context.Background()

	home := testutil.IsolatedHome(t)
	env := testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)
	env["HOME"] = home
	layout := config.NewLayout(filepath.Join(home, ".yoloai")).WithPrincipal(config.CLIPrincipal).WithEnv(env)

	rt, err := seatbelt.New(ctx, layout, home)
	require.NoError(t, err, "seatbelt backend must be available on this platform")
	t.Cleanup(func() { _ = rt.Close() })

	mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(layout))
	require.NoError(t, mgr.EnsureSetup(ctx, testutil.LogWriter(t)))
	return mgr, ctx
}

// appleMaliciousSetup builds an Engine on the apple backend. Mirrors the docker
// setup's layout split: the Engine uses an isolated-HOME layout (for sandbox
// state), while the runtime carries the curated host env it needs to reach the
// `container` CLI. Apple runs git inside the VM, so the host-side git-profile
// HOME concern does not apply.
func appleMaliciousSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	if goruntime.GOOS != "darwin" {
		t.Skip("apple backend is macOS-only")
	}
	if os.Getenv("YOLOAI_TEST_APPLE") != "1" {
		t.Skip("set YOLOAI_TEST_APPLE=1 to run the apple malicious-filter test")
	}
	ctx := context.Background()

	home := testutil.IsolatedHome(t)
	layout := config.NewLayout(filepath.Join(home, ".yoloai")).WithPrincipal(config.CLIPrincipal)

	rt, err := apple.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err, "apple container backend must be available (macOS 26 + Apple Silicon)")
	t.Cleanup(func() { _ = rt.Close() })

	mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(layout))
	require.NoError(t, mgr.EnsureSetup(ctx, testutil.LogWriter(t)))
	return mgr, ctx
}

// runMaliciousFilterAssertion creates a copy-mode sandbox, boots it when
// startTimeout > 0 (container backends whose GitExec dispatches into a running
// instance; seatbelt passes 0 and skips the boot), plants a malicious clean
// filter, stages via GenerateDiff, and asserts no host marker appears — proving
// the filter ran in-confinement, not as the host user.
func runMaliciousFilterAssertion(ctx context.Context, t *testing.T, mgr *orchestrator.Engine, name string, startTimeout time.Duration) {
	t.Helper()
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    name,
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = destroySandbox(ctx, mgr, name) })

	if startTimeout > 0 {
		_, err = startSandbox(ctx, mgr, name, orchestrator.StartOptions{})
		require.NoError(t, err)
		testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, name), startTimeout)
	}

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir(name))
	require.NoError(t, err)
	workDir := store.WorkDir(mgr.Layout().SandboxDir(name), meta.Workdir().HostPath)

	hostMarker := filepath.Join(t.TempDir(), "pwned")

	hg := git.NewTestHostWithEnv(testutil.GitEnv())
	require.NoError(t, hg.RunCmd(ctx, workDir, "config", "filter.pwn.clean",
		fmt.Sprintf("sh -c 'touch %s 2>/dev/null; cat'", hostMarker)))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".gitattributes"), []byte("evil.txt filter=pwn\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "evil.txt"), []byte("content\n"), 0600))

	_, err = copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: name, Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)

	assert.NoFileExists(t, hostMarker, "clean filter must not execute on the host (audit C1)")
}
