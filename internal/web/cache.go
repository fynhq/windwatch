package web

import (
	"context"
	"time"
)

const (
	settingsCacheTTL = 30 * time.Second
	adminCacheTTL    = 5 * time.Second
)

func (s *Server) settingsSnapshot(ctx context.Context) (map[string]string, uint64, error) {
	now := s.now()
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.settingsValues != nil && now.Before(s.settingsExpires) {
		return s.settingsValues, s.settingsVersion, nil
	}
	values, err := s.store.Settings(ctx)
	if err != nil {
		return nil, 0, err
	}
	s.settingsValues = values
	s.settingsExpires = now.Add(settingsCacheTTL)
	return values, s.settingsVersion, nil
}

func (s *Server) settings(ctx context.Context) (map[string]string, error) {
	values, _, err := s.settingsSnapshot(ctx)
	return values, err
}

func (s *Server) invalidateSettings() {
	s.settingsMu.Lock()
	s.settingsValues = nil
	s.settingsExpires = time.Time{}
	s.settingsVersion++
	s.settingsMu.Unlock()
}

func (s *Server) hasAdmin(ctx context.Context) (bool, error) {
	now := s.now()
	s.adminMu.Lock()
	if !s.adminExpires.IsZero() && now.Before(s.adminExpires) {
		value := s.adminValue
		s.adminMu.Unlock()
		return value, nil
	}
	version := s.adminVersion
	s.adminMu.Unlock()

	value, err := s.store.HasAdmin(ctx)
	if err != nil {
		return false, err
	}
	s.adminMu.Lock()
	if s.adminVersion == version {
		s.adminValue = value
		s.adminExpires = now.Add(adminCacheTTL)
	} else if !s.adminExpires.IsZero() {
		value = s.adminValue
	}
	s.adminMu.Unlock()
	return value, nil
}

func (s *Server) setAdminCache(value bool) {
	s.adminMu.Lock()
	s.adminValue = value
	s.adminExpires = s.now().Add(adminCacheTTL)
	s.adminVersion++
	s.adminMu.Unlock()
}
