package web

import (
	"testing"
	"time"
)

func TestWindowLimiterResetsLateKeyWithoutGlobalPrune(t *testing.T) {
	start := time.Unix(0, 0)
	limiter := newWindowLimiter(2, time.Minute)
	if !limiter.allow("other", start) {
		t.Fatal("initial key should be allowed")
	}
	late := start.Add(30 * time.Second)
	if !limiter.allow("late", late) || !limiter.allow("late", late.Add(time.Second)) {
		t.Fatal("late key should reach its limit")
	}
	// A request for another key just after a-minute prevents the global prune,
	// but the late key's own window has elapsed and must reset.
	if !limiter.allow("other", start.Add(61*time.Second)) {
		t.Fatal("other key should still be allowed")
	}
	if !limiter.allow("late", late.Add(time.Minute)) {
		t.Fatal("late key window should reset from its own start")
	}
}

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
