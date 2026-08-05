// ABOUTME: Tests for the apple net-health probe — the inspect parser, pinned against real
// ABOUTME: `container inspect` output, and the wedge classifier that DF172 asks for.

package apple

import (
	"net"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realInspectJSON is trimmed verbatim from `container inspect` on macOS 26.5.1
// (apple container, 2026-08-03). The nesting is the point: the address lives at
// status.networks[0], NOT next to status.state where Inspect reads from, and
// nothing in the repo documented that shape before this probe needed it.
const realInspectJSON = `[
  {
    "configuration" : {
      "id" : "yoloai-cli-df172-apple",
      "networks" : [ { "network" : "default" } ]
    },
    "status" : {
      "networks" : [
        {
          "hostname" : "yoloai-cli-df172-apple",
          "ipv4Address" : "192.168.64.6\/24",
          "ipv4Gateway" : "192.168.64.1",
          "macAddress" : "fa:e4:be:67:3c:f0",
          "network" : "default"
        }
      ],
      "state" : "running"
    }
  }
]`

func TestParseContainerNetwork_RealOutput(t *testing.T) {
	addr, gw, err := parseContainerNetwork(realInspectJSON)
	require.NoError(t, err)
	assert.Equal(t, "192.168.64.6/24", addr)
	assert.Equal(t, "192.168.64.1", gw)
}

func TestParseContainerNetwork_Rejects(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"not JSON", "not json at all"},
		{"empty array", `[]`},
		{"no networks", `[{"status":{"state":"running"}}]`},
		{"network with no address", `[{"status":{"networks":[{"network":"default"}]}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseContainerNetwork(tc.in)
			require.Error(t, err)
		})
	}
}

func ips(list ...string) []net.IP {
	out := make([]net.IP, 0, len(list))
	for _, s := range list {
		out = append(out, net.ParseIP(s))
	}
	return out
}

func TestClassifyAppleNetHealth(t *testing.T) {
	tests := []struct {
		name       string
		addr       string
		gateway    string
		hostIPs    []net.IP
		wantState  runtime.NetHealthState
		wantDetail string
	}{
		{
			// The measured healthy case: gateway 192.168.64.1 lives on bridge101.
			name:       "gateway is on a host interface — ok",
			addr:       "192.168.64.6/24",
			gateway:    "192.168.64.1",
			hostIPs:    ips("192.168.139.3", "192.168.64.1", "10.0.0.5"),
			wantState:  runtime.NetHealthOK,
			wantDetail: "192.168.64.6",
		},
		{
			// DF172's actual failure: the bridge was re-created on 192.168.65.x
			// while this container kept its 192.168.64.x epoch. It still reports
			// running and still holds an address; nothing answers its gateway.
			name:      "gateway answered by no host interface — wedged",
			addr:      "192.168.64.6/24",
			gateway:   "192.168.64.1",
			hostIPs:   ips("192.168.139.3", "192.168.65.1"),
			wantState: runtime.NetHealthWedged,
		},
		{
			name:      "gateway outside the container's own subnet — wedged",
			addr:      "192.168.64.6/24",
			gateway:   "192.168.65.1",
			hostIPs:   ips("192.168.65.1"),
			wantState: runtime.NetHealthWedged,
		},
		{
			// A failed enumeration must never read as a wedge: it would turn one
			// transient error into a fleet-wide false alarm.
			name:      "host interfaces unenumerable — unknown, not wedged",
			addr:      "192.168.64.6/24",
			gateway:   "192.168.64.1",
			hostIPs:   nil,
			wantState: runtime.NetHealthUnknown,
		},
		{
			name:      "unparseable address — unknown",
			addr:      "not-an-address",
			gateway:   "192.168.64.1",
			hostIPs:   ips("192.168.64.1"),
			wantState: runtime.NetHealthUnknown,
		},
		{
			name:      "unparseable gateway — unknown",
			addr:      "192.168.64.6/24",
			gateway:   "",
			hostIPs:   ips("192.168.64.1"),
			wantState: runtime.NetHealthUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, detail := classifyAppleNetHealth(tc.addr, tc.gateway, nil, tc.hostIPs)
			assert.Equal(t, tc.wantState, state, "detail was: %s", detail)
			if tc.wantDetail != "" {
				assert.Equal(t, tc.wantDetail, detail)
			}
			assert.NotEmpty(t, detail, "every verdict must carry a human-readable reason")
		})
	}
}

// A parse failure is reported, not swallowed into a healthy verdict.
func TestClassifyAppleNetHealth_ParseErrorIsUnknown(t *testing.T) {
	state, detail := classifyAppleNetHealth("", "", assert.AnError, ips("192.168.64.1"))
	assert.Equal(t, runtime.NetHealthUnknown, state)
	assert.Contains(t, detail, assert.AnError.Error())
}
