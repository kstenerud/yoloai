package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatBytes_Bytes(t *testing.T) {
	assert.Equal(t, "500 B", FormatBytes(500))
	assert.Equal(t, "0 B", FormatBytes(0))
	assert.Equal(t, "1023 B", FormatBytes(1023))
}

func TestFormatBytes_MB(t *testing.T) {
	assert.Equal(t, "1.0 MB", FormatBytes(1024*1024))
	assert.Equal(t, "5.0 MB", FormatBytes(5*1024*1024))
	assert.Equal(t, "1.5 MB", FormatBytes(1024*1024+512*1024))
}

func TestFormatBytes_GB(t *testing.T) {
	assert.Equal(t, "1.00 GB", FormatBytes(1024*1024*1024))
	assert.Equal(t, "2.50 GB", FormatBytes(2*1024*1024*1024+512*1024*1024))
}
