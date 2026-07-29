// ABOUTME: Docker build-secret handling for profile builds: auto-detecting a
// ABOUTME: host ~/.npmrc, and parsing/validating id=,src= specs (order, missing
// ABOUTME: fields, tilde expansion, source-file existence).
package profiles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoBuildSecrets_NpmrcExists(t *testing.T) {
	home := t.TempDir()

	npmrcPath := filepath.Join(home, ".npmrc")
	require.NoError(t, os.WriteFile(npmrcPath, []byte("registry=https://npm.example.com"), 0600))

	secrets := AutoBuildSecrets(home)
	require.Len(t, secrets, 1)
	assert.Equal(t, "id=npmrc,src="+npmrcPath, secrets[0])
}

func TestAutoBuildSecrets_NpmrcMissing(t *testing.T) {
	home := t.TempDir()

	secrets := AutoBuildSecrets(home)
	assert.Nil(t, secrets)
}

func TestValidateBuildSecret_Valid(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "token.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("secret"), 0600))

	tests := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "simple",
			spec: "id=mytoken,src=" + srcFile,
			want: "id=mytoken,src=" + srcFile,
		},
		{
			name: "reversed order",
			spec: "src=" + srcFile + ",id=mytoken",
			want: "id=mytoken,src=" + srcFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateBuildSecret(tt.spec, "/home/user")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateBuildSecret_MissingID(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "token.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("secret"), 0600))

	_, err := ValidateBuildSecret("src="+srcFile, "/home/user")
	assert.ErrorContains(t, err, "missing id=")
}

func TestValidateBuildSecret_MissingSrc(t *testing.T) {
	_, err := ValidateBuildSecret("id=mytoken", "/home/user")
	assert.ErrorContains(t, err, "missing src=")
}

func TestValidateBuildSecret_FileNotFound(t *testing.T) {
	_, err := ValidateBuildSecret("id=mytoken,src=/nonexistent/path/token.txt", "/home/user")
	assert.ErrorContains(t, err, "source file not found")
}

func TestValidateBuildSecret_TildeExpansion(t *testing.T) {
	home := t.TempDir()

	npmrcPath := filepath.Join(home, ".npmrc")
	require.NoError(t, os.WriteFile(npmrcPath, []byte("registry=https://npm.example.com"), 0600))

	got, err := ValidateBuildSecret("id=npmrc,src=~/.npmrc", home)
	require.NoError(t, err)
	assert.Equal(t, "id=npmrc,src="+npmrcPath, got)
}

// The chain-checksum tests below were retargeted from
// runtime/docker's profileBuildChecksum, which the D125 gate flagged as dead
// once the scheme moved here (DF152). Deleting them with it would have dropped
// the only coverage this logic has — DF105's lesson.

func TestChainChecksum_ValidDockerfile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM yoloai-base\nRUN apt install -y go"), 0600))

	sum := chainChecksum(dir, "")
	assert.NotEmpty(t, sum)
	assert.Len(t, sum, 64, "expected SHA-256 hex string")
}

func TestChainChecksum_Deterministic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base"), 0600))

	assert.Equal(t, chainChecksum(dir, ""), chainChecksum(dir, ""))
}

func TestChainChecksum_MissingDockerfile(t *testing.T) {
	assert.Empty(t, chainChecksum(t.TempDir(), ""),
		"no Dockerfile means no checksum, which the caller reads as 'cannot vouch' and builds")
}

// TestChainChecksum_ParentChangePropagates is the behaviour the marker scheme
// expressed by comparing file modification times, and the reason chaining
// replaced it. A parent rebuild has to invalidate every descendant, at any
// depth — and it now does so by construction rather than by the filesystem
// happening to order two writes correctly.
func TestChainChecksum_ParentChangePropagates(t *testing.T) {
	child := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(child, "Dockerfile"), []byte("FROM parent"), 0600))

	before := chainChecksum(child, "parent-checksum-v1")
	after := chainChecksum(child, "parent-checksum-v2")
	assert.NotEqual(t, before, after,
		"the child's own Dockerfile is unchanged, but its parent moved — the child is stale")

	// And a grandchild inherits the invalidation, which is what "at any depth" means.
	grand := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(grand, "Dockerfile"), []byte("FROM child"), 0600))
	assert.NotEqual(t, chainChecksum(grand, before), chainChecksum(grand, after))
}
