package web

import (
	"testing"
	"time"
)

func TestWindowLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := newWindowLimiter(2, time.Minute)
	for i := 0; i < 2; i++ {
		if !limiter.allow("client", now) {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if limiter.allow("client", now) {
		t.Fatal("third request should be rejected")
	}
	if !limiter.allow("other", now) {
		t.Fatal("different key should be allowed")
	}
	if !limiter.allow("client", now.Add(time.Minute)) {
		t.Fatal("window should reset")
	}
}
