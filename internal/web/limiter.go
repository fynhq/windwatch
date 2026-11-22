package web

import (
	"sync"
	"time"
)

const maxLimiterKeys = 4096

type windowLimiter struct {
	mu       sync.Mutex
	limit    int
	maxKeys  int
	window   time.Duration
	windows  map[string]*windowEntry
	lastSeen time.Time
}

type windowEntry struct {
	start time.Time
	count int
}

func newWindowLimiter(limit int, window time.Duration) *windowLimiter {
	return &windowLimiter{
		limit:   limit,
		maxKeys: maxLimiterKeys,
		window:  window,
		windows: make(map[string]*windowEntry),
	}
}

func (l *windowLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	if entry := l.windows[key]; entry != nil {
		if now.Sub(entry.start) >= l.window {
			entry.start = now
			entry.count = 0
		}
		if entry.count >= l.limit {
			return false
		}
		entry.count++
		return true
	}
	if len(l.windows) >= l.maxKeys {
		l.pruneAllLocked(now)
		if len(l.windows) >= l.maxKeys {
			return false
		}
	}
	l.windows[key] = &windowEntry{start: now, count: 1}
	return true
}

func (l *windowLimiter) pruneLocked(now time.Time) {
	if !l.lastSeen.IsZero() && now.Sub(l.lastSeen) < l.window {
		return
	}
	l.pruneAllLocked(now)
}

func (l *windowLimiter) pruneAllLocked(now time.Time) {
	l.lastSeen = now
	for key, entry := range l.windows {
		if now.Sub(entry.start) >= l.window {
			delete(l.windows, key)
		}
	}
}
