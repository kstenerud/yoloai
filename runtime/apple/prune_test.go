// ABOUTME: Unit tests for the pure helpers backing the apple CachePruner —
// ABOUTME: JSON parsing, byte totals, and reclaim-delta math (no live CLI).
package apple

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleSystemDF = `{"containers":{"active":2,"reclaimable":0,"sizeInBytes":85005000704,"total":2},
"images":{"active":2,"reclaimable":62660550656,"sizeInBytes":70090452992,"total":8},
"volumes":{"active":0,"reclaimable":0,"sizeInBytes":0,"total":0}}`

func TestParseSystemDF(t *testing.T) {
	got, err := parseSystemDF([]byte(sampleSystemDF))
	require.NoError(t, err)
	assert.Equal(t, dfUsage{
		ImagesBytes:       70090452992,
		ContainersBytes:   85005000704,
		VolumesBytes:      0,
		ImagesReclaimable: 62660550656,
	}, got)
}

func TestParseSystemDF_Malformed(t *testing.T) {
	_, err := parseSystemDF([]byte("not json"))
	assert.Error(t, err)
}

func TestDFUsage_TotalBytes(t *testing.T) {
	d := dfUsage{ImagesBytes: 100, ContainersBytes: 20, VolumesBytes: 5}
	assert.Equal(t, int64(125), d.totalBytes())
}

func TestDryRunReclaimEstimate(t *testing.T) {
	df := dfUsage{ImagesReclaimable: 62660550656}

	// --images runs `image prune --all`, which reclaims every unused image that
	// system df counts — so the reclaimable figure is an honest estimate.
	assert.Equal(t, int64(62660550656), dryRunReclaimEstimate(df, true))

	// Plain prune removes only dangling images (bytes not broken out by df and
	// usually shared with the base), so it must not promise the reclaimable
	// figure — that would over-report what plain prune actually frees.
	assert.Equal(t, int64(0), dryRunReclaimEstimate(df, false))
}

func TestReclaimDelta(t *testing.T) {
	tests := []struct {
		name   string
		before dfUsage
		after  dfUsage
		want   int64
	}{
		{
			name:   "before greater than after: positive delta",
			before: dfUsage{ImagesBytes: 100},
			after:  dfUsage{ImagesBytes: 40},
			want:   60,
		},
		{
			name:   "after equal to before: clamped to zero",
			before: dfUsage{ImagesBytes: 100},
			after:  dfUsage{ImagesBytes: 100},
			want:   0,
		},
		{
			name:   "after greater than before: clamped to zero",
			before: dfUsage{ImagesBytes: 100},
			after:  dfUsage{ImagesBytes: 150},
			want:   0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, reclaimDelta(tc.before, tc.after))
		})
	}
}
