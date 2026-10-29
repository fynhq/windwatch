package web

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

const (
	pushIPWindowDuration = time.Minute
	pushIPLimit          = 300
)

type pushIPWindow struct {
	start time.Time
	count int
}

type pushLimiter struct {
	mu       sync.Mutex
	last     map[string]time.Time
	ipWindow map[string]*pushIPWindow
	lastSeen time.Time
}

func newPushLimiter() *pushLimiter {
	return &pushLimiter{
		last:     make(map[string]time.Time),
		ipWindow: make(map[string]*pushIPWindow),
	}
}

func (l *pushLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.last[key]; ok && now.Sub(last) < time.Second {
		return false
	}
	l.last[key] = now
	return true
}

func (l *pushLimiter) allowIP(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	entry := l.ipWindow[key]
	if entry == nil || now.Sub(entry.start) >= pushIPWindowDuration {
		l.ipWindow[key] = &pushIPWindow{start: now, count: 1}
		return true
	}
	if entry.count >= pushIPLimit {
		return false
	}
	entry.count++
	return true
}

func (l *pushLimiter) pruneLocked(now time.Time) {
	if !l.lastSeen.IsZero() && now.Sub(l.lastSeen) < pushIPWindowDuration {
		return
	}
	l.lastSeen = now
	for key, entry := range l.ipWindow {
		if now.Sub(entry.start) >= pushIPWindowDuration {
			delete(l.ipWindow, key)
		}
	}
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if !s.pushLimiter.allowIP(s.clientKey(r), now) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	token := r.PathValue("token")
	if token == "" || len(token) > 200 {
		http.NotFound(w, r)
		return
	}
	hash := secret.HashToken(token)
	pushToken, err := s.store.PushTokenByHash(r.Context(), hash)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	monitor, err := s.store.MonitorByID(r.Context(), pushToken.MonitorID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (monitor.Type != model.MonitorTypePush || !monitor.Enabled)) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !s.pushLimiter.allow(hash, now) {
		http.Error(w, "too many heartbeats", http.StatusTooManyRequests)
		return
	}
	if err := s.store.RecordHeartbeat(r.Context(), monitor.ID, now.UTC()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}
