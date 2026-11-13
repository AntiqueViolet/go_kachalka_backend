package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	cases := []struct {
		name   string
		remote string
		xff    string
		want   string
	}{
		{"direct client", "203.0.113.7:5000", "", "203.0.113.7"},
		{"untrusted peer cannot spoof", "203.0.113.7:5000", "1.2.3.4", "203.0.113.7"},
		{"trusted proxy", "172.18.0.2:5000", "198.51.100.9", "198.51.100.9"},
		{"client-supplied prefix ignored", "172.18.0.2:5000", "1.2.3.4, 198.51.100.9", "198.51.100.9"},
		{"chain of trusted proxies", "172.18.0.2:5000", "198.51.100.9, 172.18.0.3", "198.51.100.9"},
		{"trusted proxy without header", "172.18.0.2:5000", "", "172.18.0.2"},
		{"ipv4-mapped ipv6", "[::ffff:203.0.113.7]:5000", "", "203.0.113.7"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r, trusted).String(); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestLimitKeyGroupsIPv6By64(t *testing.T) {
	a := limitKey(netip.MustParseAddr("2001:db8:1:2:aaaa::1"))
	b := limitKey(netip.MustParseAddr("2001:db8:1:2:bbbb::9"))
	if a != b || a != "2001:db8:1:2::/64" {
		t.Fatalf("keys %q and %q, want both 2001:db8:1:2::/64", a, b)
	}
	if got := limitKey(netip.MustParseAddr("2001:db8:1:3::1")); got == a {
		t.Fatal("different /64 networks share a key")
	}
	if got := limitKey(netip.MustParseAddr("203.0.113.7")); got != "203.0.113.7" {
		t.Fatalf("IPv4 key %q", got)
	}
}
