package cliutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatAge_Seconds(t *testing.T) {
	created := time.Now().Add(-30 * time.Second)
	assert.Equal(t, "30s", FormatAge(created))
}

func TestFormatAge_Minutes(t *testing.T) {
	created := time.Now().Add(-5 * time.Minute)
	assert.Equal(t, "5m", FormatAge(created))
}

func TestFormatAge_Hours(t *testing.T) {
	created := time.Now().Add(-2 * time.Hour)
	assert.Equal(t, "2h", FormatAge(created))
}

func TestFormatAge_Days(t *testing.T) {
	created := time.Now().Add(-3 * 24 * time.Hour)
	assert.Equal(t, "3d", FormatAge(created))
}

func TestFormatSize(t *testing.T) {
	assert.Equal(t, "512B", FormatSize(512))
	assert.Equal(t, "2KB", FormatSize(2*1024))
	assert.Equal(t, "1.5MB", FormatSize(3*1024*1024/2))
	assert.Equal(t, "2.0GB", FormatSize(2*1024*1024*1024))
}
