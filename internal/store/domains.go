package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func (s *Store) UpsertDomainInfo(ctx context.Context, info model.DomainInfo) error {
	statuses, err := json.Marshal(info.Statuses)
	if err != nil {
		return fmt.Errorf("marshal domain statuses: %w", err)
	}
	var expiresAt any
	if !info.ExpiresAt.IsZero() {
		expiresAt = info.ExpiresAt.UTC().UnixMilli()
	}
	fetchedAt := info.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO domain_info (domain, registrar, status_json, expires_at_ms, fetched_at_ms, error)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(domain) DO UPDATE SET
			registrar = excluded.registrar,
			status_json = excluded.status_json,
			expires_at_ms = excluded.expires_at_ms,
			fetched_at_ms = excluded.fetched_at_ms,
			error = excluded.error
	`,
		info.Domain, info.Registrar, string(statuses), expiresAt, fetchedAt.UTC().UnixMilli(), info.Error,
	)
	if err != nil {
		return fmt.Errorf("upsert domain info: %w", err)
	}
	return nil
}

func (s *Store) DomainInfo(ctx context.Context, domain string) (model.DomainInfo, error) {
	var (
		info       model.DomainInfo
		statusJSON string
		expiresAt  sql.NullInt64
		fetchedAt  int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT domain, registrar, status_json, expires_at_ms, fetched_at_ms, error
		FROM domain_info WHERE domain = ?
	`, domain).Scan(
		&info.Domain, &info.Registrar, &statusJSON, &expiresAt, &fetchedAt, &info.Error,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DomainInfo{}, ErrNotFound
	}
	if err != nil {
		return model.DomainInfo{}, fmt.Errorf("get domain info: %w", err)
	}
	_ = json.Unmarshal([]byte(statusJSON), &info.Statuses)
	if expiresAt.Valid {
		info.ExpiresAt = time.UnixMilli(expiresAt.Int64).UTC()
	}
	info.FetchedAt = time.UnixMilli(fetchedAt).UTC()
	return info, nil
}

func (s *Store) DeleteDomainInfoBefore(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM domain_info WHERE fetched_at_ms < ?
	`, before.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete old domain info: %w", err)
	}
	return nil
}
