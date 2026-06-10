// ABOUTME: Tests for the container-system alias resolver — the orbstack /
// ABOUTME: docker-desktop → (docker, pinned DOCKER_HOST) mapping.

package runtime

import "testing"

func TestResolveContainerSystem(t *testing.T) {
	const home = "/Users/tester"
	cases := []struct {
		name     string
		id       BackendType
		home     string
		wantBE   BackendType
		wantHost string
	}{
		{"orbstack pins its socket", ContainerSystemOrbstack, home, BackendDocker, "unix://" + home + "/.orbstack/run/docker.sock"},
		{"docker-desktop pins its socket", ContainerSystemDockerDesktop, home, BackendDocker, "unix://" + home + "/.docker/run/docker.sock"},
		{"alias without home resolves to docker but cannot pin", ContainerSystemOrbstack, "", BackendDocker, ""},
		{"plain docker passes through, no pin", BackendDocker, home, BackendDocker, ""},
		{"podman passes through, no pin", BackendPodman, home, BackendPodman, ""},
		{"apple passes through, no pin", BackendApple, home, BackendApple, ""},
		{"empty passes through", "", home, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			be, host := ResolveContainerSystem(c.id, c.home)
			if be != c.wantBE {
				t.Errorf("backend = %q, want %q", be, c.wantBE)
			}
			if host != c.wantHost {
				t.Errorf("dockerHost = %q, want %q", host, c.wantHost)
			}
		})
	}
}

func TestIsContainerSystemAlias(t *testing.T) {
	aliases := []BackendType{ContainerSystemOrbstack, ContainerSystemDockerDesktop}
	for _, a := range aliases {
		if !IsContainerSystemAlias(a) {
			t.Errorf("IsContainerSystemAlias(%q) = false, want true", a)
		}
	}
	notAliases := []BackendType{BackendDocker, BackendPodman, BackendApple, BackendTart, ""}
	for _, n := range notAliases {
		if IsContainerSystemAlias(n) {
			t.Errorf("IsContainerSystemAlias(%q) = true, want false", n)
		}
	}
}

func TestContainerSystemsOrderAndLabels(t *testing.T) {
	got := ContainerSystems()
	want := []BackendType{ContainerSystemOrbstack, ContainerSystemDockerDesktop}
	if len(got) != len(want) {
		t.Fatalf("ContainerSystems() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ContainerSystems()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if ContainerSystemLabel(ContainerSystemOrbstack) != "OrbStack" {
		t.Errorf("label orbstack = %q", ContainerSystemLabel(ContainerSystemOrbstack))
	}
	if ContainerSystemLabel(ContainerSystemDockerDesktop) != "Docker Desktop" {
		t.Errorf("label docker-desktop = %q", ContainerSystemLabel(ContainerSystemDockerDesktop))
	}
	if got := ContainerSystemLabel(BackendPodman); got != "podman" {
		t.Errorf("label passthrough = %q, want podman", got)
	}
}
