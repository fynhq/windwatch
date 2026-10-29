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

func (s *Store) UpsertCertificate(ctx context.Context, info model.CertificateInfo) error {
	if info.Fingerprint == "" {
		return nil
	}
	sans, err := json.Marshal(info.SANs)
	if err != nil {
		return fmt.Errorf("marshal certificate SANs: %w", err)
	}
	var notBefore any
	if !info.NotBefore.IsZero() {
		notBefore = info.NotBefore.UTC().UnixMilli()
	}
	var notAfter any
	if !info.NotAfter.IsZero() {
		notAfter = info.NotAfter.UTC().UnixMilli()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO cert_info (fingerprint, subject, issuer, sans_json, not_before_ms, not_after_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET
			subject = excluded.subject,
			issuer = excluded.issuer,
			sans_json = excluded.sans_json,
			not_before_ms = excluded.not_before_ms,
			not_after_ms = excluded.not_after_ms,
			updated_at_ms = excluded.updated_at_ms
	`,
		info.Fingerprint, info.Subject, info.Issuer, string(sans),
		notBefore, notAfter, time.Now().UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("upsert certificate: %w", err)
	}
	return nil
}

func (s *Store) LatestCertificate(ctx context.Context, monitorID int64) (model.CertificateInfo, error) {
	var (
		info      model.CertificateInfo
		sansJSON  string
		notBefore sql.NullInt64
		notAfter  sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT ci.fingerprint, ci.subject, ci.issuer, ci.sans_json, ci.not_before_ms, ci.not_after_ms
		FROM checks c
		JOIN cert_info ci ON ci.fingerprint = c.cert_fingerprint
		WHERE c.monitor_id = ? AND c.cert_fingerprint IS NOT NULL
		ORDER BY c.checked_at_ms DESC
		LIMIT 1
	`, monitorID).Scan(
		&info.Fingerprint, &info.Subject, &info.Issuer, &sansJSON, &notBefore, &notAfter,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CertificateInfo{}, ErrNotFound
	}
	if err != nil {
		return model.CertificateInfo{}, fmt.Errorf("get latest certificate: %w", err)
	}
	_ = json.Unmarshal([]byte(sansJSON), &info.SANs)
	if notBefore.Valid {
		info.NotBefore = time.UnixMilli(notBefore.Int64).UTC()
	}
	if notAfter.Valid {
		info.NotAfter = time.UnixMilli(notAfter.Int64).UTC()
	}
	return info, nil
}

func (s *Store) DeleteUnreferencedCertificates(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM cert_info
		WHERE updated_at_ms < ?
		  AND fingerprint NOT IN (
			SELECT cert_fingerprint FROM checks WHERE cert_fingerprint IS NOT NULL
		  )
	`, before.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete unreferenced certificates: %w", err)
	}
	return nil
}
