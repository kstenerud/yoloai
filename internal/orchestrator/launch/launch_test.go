// ABOUTME: Tests for the launch package: secrets-consumed wait, port binding
// ABOUTME: parsing, resource limit parsing, and instance config construction.
package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

func TestGvisorStartHint(t *testing.T) {
	base := errors.New("create container: Error response from daemon: " +
		"error while looking up the specified runtime path: exec: \"/usr/local/bin/runsc\": no such file or directory")
	tmpErr := errors.New("OCI runtime create failed: cannot read client sync file: EOF")

	// Non-enhanced isolation: passes through untouched, preserving the chain.
	got := gvisorStartHint(runtime.IsolationModeContainer, base)
	assert.Equal(t, base, got, "non-enhanced must not be augmented")

	// Enhanced + runsc-missing signature: install-in-VM hint, wraps the original.
	got = gvisorStartHint(runtime.IsolationModeContainerEnhanced, base)
	assert.ErrorIs(t, got, base)
	assert.Contains(t, got.Error(), "install runsc")
	assert.Contains(t, got.Error(), "Docker VM")

	// Enhanced + /tmp-chroot signature: OrbStack /tmp hint.
	got = gvisorStartHint(runtime.IsolationModeContainerEnhanced, tmpErr)
	assert.ErrorIs(t, got, tmpErr)
	assert.Contains(t, got.Error(), "/tmp")
	assert.Contains(t, got.Error(), "OrbStack")

	// Enhanced but an unrelated error: untouched.
	other := fmt.Errorf("some unrelated failure")
	assert.Equal(t, other, gvisorStartHint(runtime.IsolationModeContainerEnhanced, other))

	// nil stays nil.
	assert.NoError(t, gvisorStartHint(runtime.IsolationModeContainerEnhanced, nil))
}

// TestMissingImageHint covers DF154: the staleness marker records that a build
// once ran, never that the image still exists, so a pruned or backend-switched
// image reaches Create as a tag the store does not have. The backend then tries
// a registry, and the user gets a pull error for something that was never in a
// registry. The hint has to name the actual cause and the actual remedy.
func TestMissingImageHint(t *testing.T) {
	const ref = "yoloai-cli-dev"
	notFound := errors.New("create container: Error response from daemon: No such image: " + ref + ":latest")

	// The real case: names the image, says why the record disagreed, gives the command.
	got := missingImageHint(ref, "dev", notFound)
	assert.ErrorIs(t, got, notFound, "the original error must stay in the chain")
	assert.Contains(t, got.Error(), ref)
	assert.Contains(t, got.Error(), "yoloai system build dev",
		"the remedy names the profile, since that is the argument the user cannot guess")
	assert.Contains(t, got.Error(), "never in a registry",
		"the pull error is the symptom; saying so is the point of the hint")

	// No profile (base image): still actionable, without a bogus argument.
	got = missingImageHint(ref, "", notFound)
	assert.Contains(t, got.Error(), "yoloai system build")
	assert.NotContains(t, got.Error(), "yoloai system build ",
		"a sandbox with no profile must not be told to build the empty-named one")

	// A not-found for a DIFFERENT image is not this sandbox's problem.
	other := errors.New("No such image: some-other-image")
	assert.Equal(t, other, missingImageHint(ref, "dev", other))

	// An unrelated failure that happens to mention the image passes through:
	// "not found" alone is far too common to key on.
	unrelated := errors.New("create container: port 8080 already allocated for " + ref)
	assert.Equal(t, unrelated, missingImageHint(ref, "dev", unrelated))

	// Degenerate inputs stay degenerate.
	assert.NoError(t, missingImageHint(ref, "dev", nil))
	assert.Equal(t, notFound, missingImageHint("", "dev", notFound),
		"with no image ref there is nothing to match against, so claim nothing")
}

// TestEffectiveSecretsConsumedTimeout verifies the host honors a backend's
// declared wait budget (slow-booting backends raise it so the secrets dir
// isn't removed before the guest reads it) and falls back to the package
// default otherwise.
func TestEffectiveSecretsConsumedTimeout(t *testing.T) {
	assert.Equal(t, secretsConsumedTimeout, effectiveSecretsConsumedTimeout(runtime.BackendDescriptor{}),
		"no backend override → package default")
	assert.Equal(t, 90*time.Second, effectiveSecretsConsumedTimeout(runtime.BackendDescriptor{SecretsConsumedTimeout: 90 * time.Second}),
		"backend-declared cap is honored")
}

// TestWaitForSecretsConsumed_ReturnsWhenMarkerExists verifies the wait
// completes promptly once the marker the in-sandbox entrypoint writes
// appears — the path that lets the host remove the secrets temp dir only
// after the guest has read it.
func TestWaitForSecretsConsumed_ReturnsWhenMarkerExists(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed")
	require.NoError(t, os.WriteFile(marker, nil, 0600))

	start := time.Now()
	waitForSecretsConsumed(marker, 5*time.Second)
	assert.Less(t, time.Since(start), time.Second,
		"should return almost immediately when the marker is already present")
}

// TestWaitForSecretsConsumed_ReturnsWhenMarkerAppears verifies the poll
// observes a marker written after the wait starts (the real ordering: the
// guest boots, reads secrets, then writes the marker while the host polls).
func TestWaitForSecretsConsumed_ReturnsWhenMarkerAppears(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed")

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = os.WriteFile(marker, nil, 0600)
	}()

	start := time.Now()
	waitForSecretsConsumed(marker, 5*time.Second)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "should have waited for the marker")
	assert.Less(t, elapsed, 2*time.Second, "should return soon after the marker appears")
}

// TestWaitForSecretsConsumed_TimesOut verifies the wait gives up after the
// timeout rather than blocking forever — the safety net that guarantees the
// ephemeral secrets dir is always removed even if a guest never signals.
func TestWaitForSecretsConsumed_TimesOut(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, ".secrets-consumed") // never created

	start := time.Now()
	waitForSecretsConsumed(marker, 250*time.Millisecond)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "should wait out the full timeout")
	assert.Less(t, elapsed, 2*time.Second, "should not block much past the timeout")
}

func TestParsePortBindings_Valid(t *testing.T) {
	mappings, err := parsePortBindings([]string{"3000:3000", "8080:80"})
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	assert.Equal(t, runtime.PortMapping{HostPort: 3000, ContainerPort: 3000, Protocol: "tcp"}, mappings[0])
	assert.Equal(t, runtime.PortMapping{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}, mappings[1])
}

func TestParsePortBindings_Invalid(t *testing.T) {
	_, err := parsePortBindings([]string{"invalid"})
	require.Error(t, err)
}

func TestParsePortBindings_Empty(t *testing.T) {
	mappings, err := parsePortBindings(nil)
	require.NoError(t, err)
	assert.Nil(t, mappings)
}

func TestParseResourceLimits(t *testing.T) {
	tests := []struct {
		name    string
		input   *config.ResourceLimits
		wantCPU int64
		wantMem int64
		wantNil bool
		wantErr bool
	}{
		{
			name:    "both set",
			input:   &config.ResourceLimits{CPUs: "4", Memory: "8g"},
			wantCPU: 4_000_000_000,
			wantMem: 8 * 1024 * 1024 * 1024,
		},
		{
			name:    "cpus only",
			input:   &config.ResourceLimits{CPUs: "2.5"},
			wantCPU: 2_500_000_000,
		},
		{
			name:    "memory only",
			input:   &config.ResourceLimits{Memory: "512m"},
			wantMem: 512 * 1024 * 1024,
		},
		{
			name:    "neither set",
			input:   &config.ResourceLimits{},
			wantNil: true,
		},
		{
			name:    "invalid cpus",
			input:   &config.ResourceLimits{CPUs: "abc"},
			wantErr: true,
		},
		{
			name:    "invalid memory",
			input:   &config.ResourceLimits{Memory: "xyz"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkResourceLimits(t, tt.input, tt.wantCPU, tt.wantMem, tt.wantNil, tt.wantErr)
		})
	}
}

// checkResourceLimits is the assertion body for a single TestParseResourceLimits case.
func checkResourceLimits(t *testing.T, input *config.ResourceLimits, wantCPU, wantMem int64, wantNil, wantErr bool) {
	t.Helper()
	result, err := parseResourceLimits(input)
	if wantErr {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)
	if wantNil {
		require.Nil(t, result)
		return
	}
	require.NotNil(t, result)
	require.Equal(t, wantCPU, result.NanoCPUs, "NanoCPUs")
	require.Equal(t, wantMem, result.Memory, "Memory")
}

func TestParseMemoryString(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"1g", 1024 * 1024 * 1024, false},
		{"512m", 512 * 1024 * 1024, false},
		{"1024k", 1024 * 1024, false},
		{"1048576b", 1048576, false},
		{"1048576", 1048576, false},        // no suffix = bytes
		{"0.5g", 512 * 1024 * 1024, false}, // fractional
		{"", 0, false},
		{"abc", 0, true},
		{"-1g", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseMemoryString(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseMemoryString(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestBuildInstanceConfig_RejectsNetworkIsolatedWithGvisor verifies that
// requesting --network-isolated together with --isolation=container-enhanced
// (gVisor) is rejected at sandbox-creation time with a specific, actionable
// error rather than producing a sandbox that lies about being isolated.
//
// gVisor's userspace netstack does not honor in-sandbox iptables rules, so
// the current entrypoint-based enforcement is a no-op there. Until the
// host-side filtering redesign lands (see docs/contributors/design/network-isolation.md)
// this combination must fail loudly.
func TestBuildInstanceConfig_RejectsNetworkIsolatedWithGvisor(t *testing.T) {
	st := &state.State{
		Name:        "test",
		Workdir:     &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:       agent.GetAgent("test"),
		NetworkMode: "isolated",
		Isolation:   "container-enhanced",
		Layout:      config.Layout{Principal: config.CLIPrincipal},
	}

	_, err := buildInstanceConfig(runtime.BackendDescriptor{Type: "mock", Capabilities: runtime.BackendCaps{NetworkIsolation: true}}, st, nil, nil, brokerOutcome{}, false, "")
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "container-enhanced", "error names the broken isolation mode")
	assert.Contains(t, msg, "gVisor", "error explains why")
	assert.Contains(t, msg, "--isolation=container", "error points at the working alternatives")
}

func TestBuildInstanceConfig_RejectsAppleContainerNetworkNoneEvenWithSystemDNS(t *testing.T) {
	st := &state.State{
		Name:        "test",
		Workdir:     &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:       agent.GetAgent("test"),
		NetworkMode: "none",
		Layout:      config.Layout{Principal: config.CLIPrincipal},
	}
	_, err := buildInstanceConfig(runtime.BackendDescriptor{Type: runtime.BackendApple}, st, nil, nil, brokerOutcome{}, false, "")
	assert.ErrorContains(t, err, "does not support --network-none")
}

func TestBuildInstanceConfig_MapsOrderedDNSAndRejectsUnsupportedCustomDNS(t *testing.T) {
	st := &state.State{
		Name:    "test",
		Workdir: &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:   agent.GetAgent("test"),
		DNS:     []string{"1.1.1.1", "8.8.8.8"},
		Layout:  config.Layout{Principal: config.CLIPrincipal},
	}
	apple := runtime.BackendDescriptor{Type: runtime.BackendApple, Capabilities: runtime.BackendCaps{CustomDNS: true}}
	cfg, err := buildInstanceConfig(apple, st, nil, nil, brokerOutcome{}, false, "")
	require.NoError(t, err)
	assert.Equal(t, st.DNS, cfg.DNS)

	_, err = buildInstanceConfig(runtime.BackendDescriptor{Type: runtime.BackendDocker}, st, nil, nil, brokerOutcome{}, false, "")
	assert.ErrorContains(t, err, "custom DNS is not supported")
	st.NetworkMode = "none"
	_, err = buildInstanceConfig(apple, st, nil, nil, brokerOutcome{}, false, "")
	assert.ErrorContains(t, err, "does not support --network-none")
	_, err = buildInstanceConfig(runtime.BackendDescriptor{
		Type: runtime.BackendDocker, Capabilities: runtime.BackendCaps{CustomDNS: true},
	}, st, nil, nil, brokerOutcome{}, false, "")
	assert.ErrorContains(t, err, "custom DNS is not supported")
}

// TestBuildInstanceConfig_BrokerOutcome verifies the broker outcome overrides the
// container's network mode (rootless podman → slirp) and publishes the injector
// endpoint as YOLOAI_BROKER_INJECTOR_ENDPOINT, while leaving both untouched when
// brokering didn't engage.
func TestBuildInstanceConfig_BrokerOutcome(t *testing.T) {
	st := &state.State{
		Name:    "test",
		Workdir: &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
		Agent:   agent.GetAgent("test"),
		Layout:  config.Layout{Principal: config.CLIPrincipal},
	}
	desc := runtime.BackendDescriptor{Type: "mock"}

	cfg, err := buildInstanceConfig(desc, st, nil, nil, brokerOutcome{}, false, "")
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Hostname, "hostname is the sanitized sandbox name, not the instance id")
	assert.Equal(t, "", cfg.NetworkMode, "no broker: keep the user's network mode")
	assert.NotContains(t, cfg.ContainerEnv, "YOLOAI_BROKER_INJECTOR_ENDPOINT=", "no broker: no injector env")

	cfg, err = buildInstanceConfig(desc, st, nil, nil, brokerOutcome{
		NetworkMode:      "slirp4netns:allow_host_loopback=true",
		InjectorEndpoint: "172.17.0.1:44115",
	}, false, "")
	require.NoError(t, err)
	assert.Equal(t, "slirp4netns:allow_host_loopback=true", cfg.NetworkMode, "broker mode overrides")
	assert.Contains(t, cfg.ContainerEnv, "YOLOAI_BROKER_INJECTOR_ENDPOINT=172.17.0.1:44115", "injector endpoint published")
}

// TestBuildInstanceConfig_AllowsNetworkIsolatedOnSupportedModes is the
// counterpart: every isolation mode that yoloai claims to support with
// --network-isolated must build a config without error. If a future change
// to IsolationEnforcesInSandboxIptables incorrectly excludes a working mode,
// this test catches the over-rejection.
func TestBuildInstanceConfig_AllowsNetworkIsolatedOnSupportedModes(t *testing.T) {
	supported := []runtime.IsolationMode{"", "container", "container-privileged", "vm", "vm-enhanced"}
	for _, isolation := range supported {
		t.Run("isolation="+string(isolation), func(t *testing.T) {
			st := &state.State{
				Name:        "test",
				Workdir:     &state.DirSpec{Path: "/project", Mode: store.DirMode("copy")},
				Agent:       agent.GetAgent("test"),
				NetworkMode: "isolated",
				Isolation:   isolation,
				Layout:      config.Layout{Principal: config.CLIPrincipal},
			}
			_, err := buildInstanceConfig(runtime.BackendDescriptor{Type: "mock", Capabilities: runtime.BackendCaps{NetworkIsolation: true, CapAdd: true}}, st, nil, nil, brokerOutcome{}, false, "")
			require.NoError(t, err)
		})
	}
}

// TestInstanceLabels: both labels are stamped for every principal. The
// omits-for-the-default-principal case this used to assert went with D126 —
// there is no default principal to elide, and instanceLabels("") is now
// unreachable (ParsePrincipalSegment rejects the empty string at the boundary
// and InstancePrefix panics on it), so pinning its output pinned a scenario that
// cannot occur.
func TestInstanceLabels(t *testing.T) {
	for _, principal := range []config.PrincipalSegment{config.CLIPrincipal, "acme"} {
		t.Run("principal="+string(principal), func(t *testing.T) {
			labels := instanceLabels(principal, "mybox", "")
			assert.Equal(t, "mybox", labels[runtime.LabelSandbox])
			assert.Equal(t, string(principal), labels[runtime.LabelPrincipal],
				"every instance carries its owner, so a sweep never has to infer one")
		})
	}
}

// TestInstanceLabels_StampsImageLineage pins DF156's create half. The instance
// has to carry which yoloai-base its image descends from, because after the
// container exists that is the only record still able to answer the question —
// the tag it was created by can be re-pointed by any later rebuild, and on two
// of the three container backends the image it named is not even resolvable.
//
// Empty stays absent rather than becoming an empty-valued label: callers read a
// missing label as "out of date", and a present-but-empty one would claim a
// lineage of nothing and read as a mismatch for a different reason.
func TestInstanceLabels_StampsImageLineage(t *testing.T) {
	stamped := instanceLabels(config.CLIPrincipal, "mybox", "base-abc123")
	assert.Equal(t, "base-abc123", stamped[runtime.BaseChecksumLabel],
		"the lineage the sandbox is judged on after it exists")

	unknown := instanceLabels(config.CLIPrincipal, "mybox", "")
	assert.NotContains(t, unknown, runtime.BaseChecksumLabel,
		"nothing known: leave it absent so the absence rule applies, rather than asserting an empty lineage")
}

// recoveryRuntime is a fakeRuntime whose Create is scripted per call, so a test
// can express "fails missing the first time, succeeds the second".
type recoveryRuntime struct {
	fakeRuntime
	errs    []error // consumed in order; nil means success
	creates int
}

func (r *recoveryRuntime) Create(_ context.Context, _ runtime.InstanceConfig) error {
	i := r.creates
	r.creates++
	if i < len(r.errs) {
		return r.errs[i]
	}
	return nil
}

// TestCreateWithImageRecovery covers DF154's recovery half: a profile image the
// marker vouches for but the store does not have is rebuilt and retried once,
// rather than surfacing a registry pull error for a tag that was never in a
// registry.
func TestCreateWithImageRecovery(t *testing.T) {
	const ref = "yoloai-cli-dev"
	missing := errors.New("create container: Error response from daemon: No such image: " + ref)
	cfg := runtime.InstanceConfig{ImageRef: ref}

	// Swap the rebuild for a recorder; restore after each subtest.
	type rebuildCall struct {
		profile string
		force   bool
	}
	install := func(t *testing.T, result error, calls *[]rebuildCall) {
		t.Helper()
		prev := rebuildProfileImage
		rebuildProfileImage = func(_ context.Context, _ runtime.Backend, _ config.Layout, profile string,
			_ []string, _ io.Writer, _ *slog.Logger, force bool) error {
			*calls = append(*calls, rebuildCall{profile, force})
			return result
		}
		t.Cleanup(func() { rebuildProfileImage = prev })
	}
	st := func() *state.State {
		return &state.State{Profile: "dev", Output: io.Discard}
	}

	t.Run("rebuilds and retries once", func(t *testing.T) {
		var calls []rebuildCall
		install(t, nil, &calls)
		rt := &recoveryRuntime{errs: []error{missing}}

		require.NoError(t, createWithImageRecovery(context.Background(), rt, st(), cfg))
		assert.Equal(t, 2, rt.creates, "one failure, one retry — never more")
		require.Len(t, calls, 1)
		assert.Equal(t, "dev", calls[0].profile)
		assert.True(t, calls[0].force,
			"the marker is what lied, so the rebuild must bypass it")
	})

	t.Run("does not retry forever when the image is still missing", func(t *testing.T) {
		var calls []rebuildCall
		install(t, nil, &calls)
		rt := &recoveryRuntime{errs: []error{missing, missing}}

		err := createWithImageRecovery(context.Background(), rt, st(), cfg)
		require.Error(t, err)
		assert.Equal(t, 2, rt.creates, "exactly one retry, not a loop")
		assert.Len(t, calls, 1)
		assert.Contains(t, err.Error(), "rebuilt automatically",
			"failing twice for the same reason is a different diagnosis from failing once")
	})

	t.Run("a failed rebuild surfaces the build error, not the pull error", func(t *testing.T) {
		var calls []rebuildCall
		buildErr := errors.New("build profile image: dockerfile parse error on line 3")
		install(t, buildErr, &calls)
		rt := &recoveryRuntime{errs: []error{missing}}

		err := createWithImageRecovery(context.Background(), rt, st(), cfg)
		require.Error(t, err)
		assert.ErrorIs(t, err, buildErr, "the actionable error is the one the user must fix")
		assert.Equal(t, 1, rt.creates, "a broken Dockerfile must not be retried")
		assert.Contains(t, err.Error(), "automatic rebuild")
	})

	t.Run("an unrelated Create failure is never rebuilt", func(t *testing.T) {
		var calls []rebuildCall
		install(t, nil, &calls)
		other := errors.New("create container: port 8080 already allocated")
		rt := &recoveryRuntime{errs: []error{other}}

		err := createWithImageRecovery(context.Background(), rt, st(), cfg)
		assert.ErrorIs(t, err, other)
		assert.Equal(t, 1, rt.creates)
		assert.Empty(t, calls, "rebuilding on an unrelated failure would be a 3-minute red herring")
	})

	t.Run("no profile means the hint only, since base staleness is label-checked", func(t *testing.T) {
		var calls []rebuildCall
		install(t, nil, &calls)
		rt := &recoveryRuntime{errs: []error{missing}}
		bare := &state.State{Output: io.Discard}

		err := createWithImageRecovery(context.Background(), rt, bare, cfg)
		require.Error(t, err)
		assert.Empty(t, calls)
		assert.Contains(t, err.Error(), "yoloai system build",
			"still actionable, just not automatic")
	})
}
