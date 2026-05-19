// ABOUTME: Tests for devcontainer.json parsing, port extraction, mount filtering, and env merging.
// ABOUTME: Covers all three LifecycleCmd forms and all FilterMounts stripping rules.

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLifecycleCmd_String(t *testing.T) {
	raw := `"npm install"`
	var cmd LifecycleCmd
	require.NoError(t, json.Unmarshal([]byte(raw), &cmd))
	assert.False(t, cmd.IsZero())
	assert.Equal(t, "npm install", cmd.Raw().(string))
}

func TestLifecycleCmd_Array(t *testing.T) {
	raw := `["go", "mod", "download"]`
	var cmd LifecycleCmd
	require.NoError(t, json.Unmarshal([]byte(raw), &cmd))
	assert.False(t, cmd.IsZero())
	arr := cmd.Raw().([]string)
	assert.Equal(t, []string{"go", "mod", "download"}, arr)
}

func TestLifecycleCmd_Object(t *testing.T) {
	raw := `{"download": "go mod download", "tools": "make tools"}`
	var cmd LifecycleCmd
	require.NoError(t, json.Unmarshal([]byte(raw), &cmd))
	assert.False(t, cmd.IsZero())
	obj := cmd.Raw().(map[string]any)
	assert.Equal(t, "go mod download", obj["download"])
}

func TestLifecycleCmd_IsZero_Absent(t *testing.T) {
	var cmd LifecycleCmd
	assert.True(t, cmd.IsZero())
}

func TestExtractPorts_ForwardPorts(t *testing.T) {
	dc := &DevcontainerConfig{
		ForwardPorts: []int{3000, 8080},
	}
	ports := dc.ExtractPorts()
	assert.ElementsMatch(t, []string{"3000:3000", "8080:8080"}, ports)
}

func TestExtractPorts_AppPort(t *testing.T) {
	dc := &DevcontainerConfig{
		AppPort: []int{4000},
	}
	ports := dc.ExtractPorts()
	assert.Equal(t, []string{"4000:4000"}, ports)
}

func TestExtractPorts_Both_Dedup(t *testing.T) {
	dc := &DevcontainerConfig{
		ForwardPorts: []int{3000, 8080},
		AppPort:      []int{8080, 9090},
	}
	ports := dc.ExtractPorts()
	assert.Len(t, ports, 3) // 3000, 8080, 9090 (8080 deduped)
	assert.Contains(t, ports, "3000:3000")
	assert.Contains(t, ports, "8080:8080")
	assert.Contains(t, ports, "9090:9090")
}

func TestFilterMounts_DockerSocket(t *testing.T) {
	dc := &DevcontainerConfig{
		Mounts: []string{"/var/run/docker.sock:/var/run/docker.sock"},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	assert.Empty(t, mounts)
	assert.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "docker socket")
}

func TestFilterMounts_CredentialDir(t *testing.T) {
	dc := &DevcontainerConfig{
		Mounts: []string{"/home/user/.claude:/home/yoloai/.claude:ro"},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	assert.Empty(t, mounts)
	assert.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "credential")
}

func TestFilterMounts_WorkdirConflict(t *testing.T) {
	dc := &DevcontainerConfig{
		Mounts: []string{"/other/path:/workdir/myproject"},
	}
	mounts, warnings := dc.FilterMounts("/workdir/myproject")
	assert.Empty(t, mounts)
	assert.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "workdir")
}

func TestFilterMounts_PassThrough(t *testing.T) {
	src := t.TempDir()
	dc := &DevcontainerConfig{
		Mounts: []string{src + ":/home/yoloai/.config/sops:ro"},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	assert.Len(t, mounts, 1)
	assert.Empty(t, warnings)
}

func TestFilterMounts_ExpandLocalEnvHome(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	// Use actual home dir (must exist) as the source to pass the existence check.
	dc := &DevcontainerConfig{
		Mounts: []string{"${localEnv:HOME}:/home/user/homedir:ro"},
	}
	mounts, _ := dc.FilterMounts("/workdir")
	require.Len(t, mounts, 1)
	assert.True(t, strings.HasPrefix(mounts[0], homeDir), "expected expanded home dir in %s", mounts[0])
}

func TestFilterMounts_TypeBindFormat(t *testing.T) {
	dc := &DevcontainerConfig{
		Mounts: []string{"type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock"},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	assert.Empty(t, mounts)
	assert.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "docker socket")
}

func TestFilterMounts_MissingSourcePath(t *testing.T) {
	dc := &DevcontainerConfig{
		Mounts: []string{"/nonexistent/path/on/host:/container/path"},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	assert.Empty(t, mounts)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "source path does not exist")
}

func TestFilterMounts_SourceFirstKeyValueFormat(t *testing.T) {
	// devcontainer.json may use source=...,target=...,type=bind (source before type)
	src := t.TempDir()
	dc := &DevcontainerConfig{
		Mounts: []string{fmt.Sprintf("source=%s,target=/root/.config/sops/age,type=bind,consistency=cached", src)},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	require.Len(t, mounts, 1)
	assert.Empty(t, warnings)
	assert.Equal(t, src+":/root/.config/sops/age", mounts[0])
}

func TestFilterMounts_KeyValueReadOnly(t *testing.T) {
	src := t.TempDir()
	dc := &DevcontainerConfig{
		Mounts: []string{fmt.Sprintf("source=%s,target=/container/path,type=bind,readonly", src)},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	require.Len(t, mounts, 1)
	assert.Empty(t, warnings)
	assert.Equal(t, src+":/container/path:ro", mounts[0])
}

func TestFilterMounts_NormalizedOutput(t *testing.T) {
	// Verify that key=value mounts are always returned in host:container[:ro] format.
	src := t.TempDir()
	dc := &DevcontainerConfig{
		Mounts: []string{fmt.Sprintf("type=bind,source=%s,target=/container/path", src)},
	}
	mounts, warnings := dc.FilterMounts("/workdir")
	require.Len(t, mounts, 1)
	assert.Empty(t, warnings)
	assert.Equal(t, src+":/container/path", mounts[0])
}

func TestMergedEnv_MergeAndPrecedence(t *testing.T) {
	dc := &DevcontainerConfig{
		ContainerEnv: map[string]string{"FOO": "container", "BAR": "container"},
		RemoteEnv:    map[string]string{"FOO": "remote"},
	}
	merged := dc.MergedEnv()
	assert.Equal(t, "remote", merged["FOO"])    // remoteEnv wins
	assert.Equal(t, "container", merged["BAR"]) // only in containerEnv
}

func TestParsedRunArgs_KnownFlags(t *testing.T) {
	dc := &DevcontainerConfig{
		RunArgs: []string{"--cpus", "4", "--memory", "8g", "--cap-add", "SYS_ADMIN"},
	}
	cpus, memory, capAdd, unknownWarnings := dc.ParsedRunArgs()
	assert.Equal(t, "4", cpus)
	assert.Equal(t, "8g", memory)
	assert.Equal(t, []string{"SYS_ADMIN"}, capAdd)
	assert.Empty(t, unknownWarnings)
}

func TestParsedRunArgs_UnknownFlags(t *testing.T) {
	dc := &DevcontainerConfig{
		RunArgs: []string{"--privileged", "--network=host"},
	}
	_, _, _, unknownWarnings := dc.ParsedRunArgs()
	assert.Len(t, unknownWarnings, 2)
}

func TestPostStartCommandUsesCompose_True(t *testing.T) {
	dc := &DevcontainerConfig{}
	require.NoError(t, json.Unmarshal([]byte(`"docker compose up -d"`), &dc.PostStartCommand))
	assert.True(t, dc.PostStartCommandUsesCompose())
}

func TestPostStartCommandUsesCompose_DockerDash(t *testing.T) {
	dc := &DevcontainerConfig{}
	require.NoError(t, json.Unmarshal([]byte(`"docker-compose up -d"`), &dc.PostStartCommand))
	assert.True(t, dc.PostStartCommandUsesCompose())
}

func TestPostStartCommandUsesCompose_False(t *testing.T) {
	dc := &DevcontainerConfig{}
	require.NoError(t, json.Unmarshal([]byte(`"npm start"`), &dc.PostStartCommand))
	assert.False(t, dc.PostStartCommandUsesCompose())
}

func TestDockerComposeFilePresent_String(t *testing.T) {
	dc := &DevcontainerConfig{}
	require.NoError(t, json.Unmarshal([]byte(`{"dockerComposeFile": "docker-compose.yml"}`), dc))
	assert.True(t, dc.DockerComposeFilePresent())
}

func TestDockerComposeFilePresent_Array(t *testing.T) {
	dc := &DevcontainerConfig{}
	require.NoError(t, json.Unmarshal([]byte(`{"dockerComposeFile": ["docker-compose.yml", "docker-compose.override.yml"]}`), dc))
	assert.True(t, dc.DockerComposeFilePresent())
}

func TestDockerComposeFilePresent_Absent(t *testing.T) {
	dc := &DevcontainerConfig{}
	assert.False(t, dc.DockerComposeFilePresent())
}

func TestLoadDevcontainer_ValidFile(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"name": "test",
		"forwardPorts": [3000],
		"onCreateCommand": "npm install",
		"postStartCommand": ["npm", "start"]
	}`
	path := filepath.Join(dir, "devcontainer.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	dc, err := LoadDevcontainer(path)
	require.NoError(t, err)
	assert.Equal(t, "test", dc.Name)
	assert.Equal(t, []int{3000}, dc.ForwardPorts)
	assert.False(t, dc.OnCreateCommand.IsZero())
	assert.False(t, dc.PostStartCommand.IsZero())
}

func TestLoadDevcontainer_BuildPresent(t *testing.T) {
	dir := t.TempDir()
	content := `{"build": {"dockerfile": "Dockerfile"}}`
	path := filepath.Join(dir, "devcontainer.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))

	dc, err := LoadDevcontainer(path)
	require.NoError(t, err)
	assert.True(t, dc.BuildPresent)
}
