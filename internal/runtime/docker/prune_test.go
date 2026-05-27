package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatBytes_Bytes(t *testing.T) {
	assert.Equal(t, "500 B", formatBytes(500))
	assert.Equal(t, "0 B", formatBytes(0))
	assert.Equal(t, "1023 B", formatBytes(1023))
}

func TestFormatBytes_MB(t *testing.T) {
	assert.Equal(t, "1.0 MB", formatBytes(1024*1024))
	assert.Equal(t, "5.0 MB", formatBytes(5*1024*1024))
	assert.Equal(t, "1.5 MB", formatBytes(1024*1024+512*1024))
}

func TestFormatBytes_GB(t *testing.T) {
	assert.Equal(t, "1.00 GB", formatBytes(1024*1024*1024))
	assert.Equal(t, "2.50 GB", formatBytes(2*1024*1024*1024+512*1024*1024))
}
