package jellyfin

import (
	"net/http/httptest"
	"testing"
)

func TestClientAddressForwardedOnlyFromPrivatePeer(t *testing.T) {
	cases := []struct {
		remote string
		xff    string
		realIP string
		want   string
	}{
		{"203.0.113.10:1234", "198.51.100.7", "", "203.0.113.10"},
		{"127.0.0.1:1234", "198.51.100.7, 10.0.0.1", "", "198.51.100.7"},
		{"10.0.0.5:1234", "", "198.51.100.8", "198.51.100.8"},
		{"[::1]:1234", "198.51.100.9", "", "198.51.100.9"},
		{"198.51.100.20:1234", "198.51.100.21", "198.51.100.22", "198.51.100.20"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.realIP != "" {
			r.Header.Set("X-Real-IP", c.realIP)
		}
		if got := ClientAddress(r); got != c.want {
			t.Fatalf("ClientAddress(%s, xff=%q, real=%q) = %q, want %q", c.remote, c.xff, c.realIP, got, c.want)
		}
	}
}
