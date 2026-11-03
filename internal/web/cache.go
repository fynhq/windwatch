package web

import (
	"context"
	"time"
)

const (
	settingsCacheTTL = 30 * time.Second
	adminCacheTTL    = 5 * time.Second
)

// settings returns the settings map from a short-lived process cache. Settings
// are read by every page for timezone selection; caching keeps 404s, auth
// pages and public polling from issuing a query each time.
func (s *Server) settings(ctx context.Context) (map[string]string, error) {
	now := s.now()
	s.settingsMu.Lock()
	if s.settingsValues != nil && now.Before(s.settingsExpires) {
		values := s.settingsValues
		s.settingsMu.Unlock()
		return values, nil
	}
	version := s.settingsVersion
	s.settingsMu.Unlock()

	values, err := s.store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	s.settingsMu.Lock()
	if s.settingsVersion == version {
		s.settingsValues = values
		s.settingsExpires = now.Add(settingsCacheTTL)
	}
	s.settingsMu.Unlock()
	return values, nil
}

func (s *Server) settingsRevision() uint64 {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.settingsVersion
}

func (s *Server) invalidateSettings() {
	s.settingsMu.Lock()
	s.settingsValues = nil
	s.settingsExpires = time.Time{}
	s.settingsVersion++
	s.settingsMu.Unlock()
}

// hasAdmin caches the setup state for a few seconds. Once the first user
// exists it never becomes false again in normal operation, but the short TTL
// keeps externally edited databases from requiring a restart.
func (s *Server) hasAdmin(ctx context.Context) (bool, error) {
	now := s.now()
	s.adminMu.Lock()
	if !s.adminExpires.IsZero() && now.Before(s.adminExpires) {
		value := s.adminValue
		s.adminMu.Unlock()
		return value, nil
	}
	s.adminMu.Unlock()

	value, err := s.store.HasAdmin(ctx)
	if err != nil {
		return false, err
	}
	s.adminMu.Lock()
	s.adminValue = value
	s.adminExpires = now.Add(adminCacheTTL)
	s.adminMu.Unlock()
	return value, nil
}

func (s *Server) setAdminCache(value bool) {
	s.adminMu.Lock()
	s.adminValue = value
	s.adminExpires = s.now().Add(adminCacheTTL)
	s.adminMu.Unlock()
}
