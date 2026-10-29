//go:build unit

package ip

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":                 "203.0.113.7",
		" 203.0.113.7 ":               "203.0.113.7",
		"203.0.113.7:51234":           "203.0.113.7",
		"::ffff:203.0.113.7":          "203.0.113.7",
		"2001:db8:1:2:3:4:5:6":        "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1":        "2001:db8:1:2::/64",
		"[2001:db8:1:2:3:4:5:6]:8080": "2001:db8:1:2::/64",
		"fe80::1%eth0":                "fe80::/64",
		"":                            "",
		"unknown":                     "",
	}
	for raw, want := range cases {
		require.Equal(t, want, NetworkKey(raw), "raw=%q", raw)
	}
}

func TestIsPubliclyRoutable(t *testing.T) {
	public := []string{"203.0.113.7", "8.8.8.8", "2001:4860:4860::8888", "::ffff:8.8.8.8"}
	for _, raw := range public {
		require.True(t, IsPubliclyRoutable(raw), "raw=%q", raw)
	}
	internal := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.17.0.1", "192.168.1.10",
		"fd00::1", "fe80::1", "169.254.1.1", "0.0.0.0", "::", "224.0.0.1", "", "not-an-ip",
	}
	for _, raw := range internal {
		require.False(t, IsPubliclyRoutable(raw), "raw=%q", raw)
	}
}
