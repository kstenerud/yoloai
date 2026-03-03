package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoBuildSecrets_NpmrcExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	npmrcPath := filepath.Join(home, ".npmrc")
	require.NoError(t, os.WriteFile(npmrcPath, []byte("registry=https://npm.example.com"), 0600))

	secrets := AutoBuildSecrets()
	require.Len(t, secrets, 1)
	assert.Equal(t, "id=npmrc,src="+npmrcPath, secrets[0])
}

func TestAutoBuildSecrets_NpmrcMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	secrets := AutoBuildSecrets()
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
			got, err := ValidateBuildSecret(tt.spec)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateBuildSecret_MissingID(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "token.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("secret"), 0600))

	_, err := ValidateBuildSecret("src=" + srcFile)
	assert.ErrorContains(t, err, "missing id=")
}

func TestValidateBuildSecret_MissingSrc(t *testing.T) {
	_, err := ValidateBuildSecret("id=mytoken")
	assert.ErrorContains(t, err, "missing src=")
}

func TestValidateBuildSecret_FileNotFound(t *testing.T) {
	_, err := ValidateBuildSecret("id=mytoken,src=/nonexistent/path/token.txt")
	assert.ErrorContains(t, err, "source file not found")
}

func TestValidateBuildSecret_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	npmrcPath := filepath.Join(home, ".npmrc")
	require.NoError(t, os.WriteFile(npmrcPath, []byte("registry=https://npm.example.com"), 0600))

	got, err := ValidateBuildSecret("id=npmrc,src=~/.npmrc")
	require.NoError(t, err)
	assert.Equal(t, "id=npmrc,src="+npmrcPath, got)
}
