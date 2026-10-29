package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

const proxySelect = `
	SELECT id, name, type, address, username, password_enc, created_at_ms, updated_at_ms
	FROM proxies
`

var ErrProxyNameTaken = errors.New("proxy name already exists")

func scanProxy(row scanner) (model.Proxy, error) {
	var (
		proxy       model.Proxy
		createdAtMS int64
		updatedAtMS int64
	)
	err := row.Scan(
		&proxy.ID, &proxy.Name, &proxy.Type, &proxy.Address,
		&proxy.Username, &proxy.PasswordEnc, &createdAtMS, &updatedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Proxy{}, ErrNotFound
	}
	if err != nil {
		return model.Proxy{}, fmt.Errorf("scan proxy: %w", err)
	}
	proxy.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	proxy.UpdatedAt = time.UnixMilli(updatedAtMS).UTC()
	return proxy, nil
}

func (s *Store) ListProxies(ctx context.Context) ([]model.Proxy, error) {
	rows, err := s.db.QueryContext(ctx, proxySelect+" ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}
	defer rows.Close()

	var proxies []model.Proxy
	for rows.Next() {
		proxy, err := scanProxy(rows)
		if err != nil {
			return nil, err
		}
		proxies = append(proxies, proxy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxies: %w", err)
	}
	return proxies, nil
}

func (s *Store) ProxyByID(ctx context.Context, id int64) (model.Proxy, error) {
	return scanProxy(s.db.QueryRowContext(ctx, proxySelect+" WHERE id = ?", id))
}

func (s *Store) CreateProxy(ctx context.Context, proxy model.Proxy, now time.Time) (int64, error) {
	if err := proxy.Validate(); err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO proxies (name, type, address, username, password_enc, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		proxy.Name, proxy.Type, proxy.Address, proxy.Username, proxy.PasswordEnc,
		now.UTC().UnixMilli(), now.UTC().UnixMilli(),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return 0, ErrProxyNameTaken
		}
		return 0, fmt.Errorf("insert proxy: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read proxy id: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateProxy(ctx context.Context, proxy model.Proxy, now time.Time) error {
	if err := proxy.Validate(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE proxies
		SET name = ?, type = ?, address = ?, username = ?, password_enc = ?, updated_at_ms = ?
		WHERE id = ?
	`,
		proxy.Name, proxy.Type, proxy.Address, proxy.Username, proxy.PasswordEnc,
		now.UTC().UnixMilli(), proxy.ID,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrProxyNameTaken
		}
		return fmt.Errorf("update proxy: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update proxy rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProxy(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM proxies WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete proxy: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete proxy rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
