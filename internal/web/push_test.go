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
