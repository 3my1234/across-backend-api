package middleware

import (
	"net/netip"
	"strings"
	"testing"
)

func TestTrustedProxyChainRejectsSpoofing(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24"), netip.MustParsePrefix("2001:db8:42::/64")}
	for _, tc := range []struct{ name, peer, header, want string }{
		{"direct spoof", "203.0.113.1", "198.51.100.99", "203.0.113.1"},
		{"one hop", "10.42.0.2", "203.0.113.5", "203.0.113.5"},
		{"multiple hops", "10.42.0.2", "203.0.113.5, 10.42.0.3", "203.0.113.5"},
		{"spoofed prefix", "10.42.0.2", "198.51.100.99, 203.0.113.5", "203.0.113.5"},
		{"malformed chain", "10.42.0.2", "bad, 203.0.113.5", "10.42.0.2"},
		{"oversize chain", "10.42.0.2", strings.Repeat("1.1.1.1,", 33) + "1.1.1.1", "10.42.0.2"},
		{"ipv6", "2001:db8:42::1", "2001:db8:43::2", "2001:db8:43::2"},
		{"empty header", "10.42.0.2", "", "10.42.0.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveClientIP(netip.MustParseAddr(tc.peer), tc.header, trusted)
			if got.String() != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
