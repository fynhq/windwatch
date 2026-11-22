package web

import (
	"strconv"
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
	if !limiter.allow("other", start.Add(61*time.Second)) {
		t.Fatal("other key should still be allowed")
	}
	if !limiter.allow("late", late.Add(time.Minute)) {
		t.Fatal("late key window should reset from its own start")
	}
}

func TestLimitersBoundKeyCardinality(t *testing.T) {
	now := time.Unix(0, 0)
	window := newWindowLimiter(10, time.Minute)
	window.maxKeys = 2
	for i := 0; i < 2; i++ {
		if !window.allow(strconv.Itoa(i), now) {
			t.Fatalf("window key %d should be allowed", i)
		}
	}
	if window.allow("overflow", now) {
		t.Fatal("window limiter accepted a key beyond capacity")
	}
	if got := len(window.windows); got != 2 {
		t.Fatalf("window keys = %d", got)
	}
	if !window.allow("overflow", now.Add(2*time.Minute)) {
		t.Fatal("window limiter did not recover after expiry")
	}

	login := newLoginLimiter()
	login.maxKeys = 2
	login.failure("one", now)
	login.failure("two", now)
	if !login.blocked("overflow", now) {
		t.Fatal("login limiter did not fail closed at capacity")
	}
	if got := len(login.entries); got != 2 {
		t.Fatalf("login keys = %d", got)
	}

	push := newPushLimiter()
	push.maxKeys = 2
	if !push.allowIP("one", now) || !push.allowIP("two", now) || push.allowIP("overflow", now) {
		t.Fatal("push IP limiter did not enforce key capacity")
	}
	if !push.allow("one", now) || !push.allow("two", now) || push.allow("overflow", now) {
		t.Fatal("push token limiter did not enforce key capacity")
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
