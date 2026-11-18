package web

import (
	"testing"
	"time"
)

func TestPushIPLimiter(t *testing.T) {
	limiter := newPushLimiter()
	now := time.Now()
	for i := 0; i < pushIPLimit; i++ {
		if !limiter.allowIP("192.0.2.10", now) {
			t.Fatalf("request %d was rejected", i+1)
		}
	}
	if limiter.allowIP("192.0.2.10", now) {
		t.Fatal("over-limit request was allowed")
	}
	if !limiter.allowIP("192.0.2.10", now.Add(pushIPWindowDuration)) {
		t.Fatal("new window was rejected")
	}
}

func TestPushLimiterPrunesThrottleKeys(t *testing.T) {
	limiter := newPushLimiter()
	start := time.Now()
	if !limiter.allow("token-hash", start) {
		t.Fatal("first request was rejected")
	}
	if !limiter.allowIP("192.0.2.10", start) {
		t.Fatal("first IP request was rejected")
	}
	if !limiter.allowIP("192.0.2.10", start.Add(2*pushIPWindowDuration)) {
		t.Fatal("request after window was rejected")
	}
	if len(limiter.last) != 0 {
		t.Fatalf("stale throttle keys were not pruned: %+v", limiter.last)
	}
}
