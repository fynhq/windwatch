package web

import (
	"net/http"
	"testing"
	"time"
)

func TestClientKeyAggregatesIPv6To64(t *testing.T) {
	s := &Server{}
	cases := []struct {
		remote string
		want   string
	}{
		{"[2001:db8:1:2::5]:443", "2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff:ffff:ffff:ffff]:443", "2001:db8:1:2::/64"},
		{"[2001:db8:1:3::5]:443", "2001:db8:1:3::/64"},
		{"192.0.2.9:443", "192.0.2.9"},
		{"[::ffff:192.0.2.9]:443", "192.0.2.9"},
	}
	for _, tc := range cases {
		r := &http.Request{RemoteAddr: tc.remote}
		if got := s.clientKey(r); got != tc.want {
			t.Errorf("clientKey(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}

func TestClientKeySharesLimitWithinIPv6Subnet(t *testing.T) {
	s := &Server{limiter: newLoginLimiter()}
	now := time.Now().UTC()
	first := &http.Request{RemoteAddr: "[2001:db8:9:9::1]:443"}
	second := &http.Request{RemoteAddr: "[2001:db8:9:9::2]:443"}
	for i := 0; i < maxLoginFailures; i++ {
		s.limiter.failure(s.clientKey(first), now)
	}
	if !s.limiter.blocked(s.clientKey(second), now) {
		t.Fatal("addresses in the same /64 should share the login limit")
	}
	other := &http.Request{RemoteAddr: "[2001:db8:9:10::1]:443"}
	if s.limiter.blocked(s.clientKey(other), now) {
		t.Fatal("a different /64 should not inherit the limit")
	}
}
