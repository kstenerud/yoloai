// ABOUTME: Tests the public Environment read model's DNS snapshot conversion.
package yoloai

import (
	"encoding/json"
	"testing"

	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
)

func TestEnvironmentFromStore_ExposesCustomDNSAndOmitsSystemDNS(t *testing.T) {
	custom := environmentFromStore(&store.Environment{DNS: []string{"1.1.1.1", "8.8.8.8"}})
	assert.Equal(t, DNSResolvers{"1.1.1.1", "8.8.8.8"}, custom.DNS)
	encoded, err := json.Marshal(custom)
	assert.NoError(t, err)
	assert.Contains(t, string(encoded), `"dns":["1.1.1.1","8.8.8.8"]`)

	system := environmentFromStore(&store.Environment{})
	encoded, err = json.Marshal(system)
	assert.NoError(t, err)
	assert.NotContains(t, string(encoded), `"dns"`)
}
