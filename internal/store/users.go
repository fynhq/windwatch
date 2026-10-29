package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

var (
	ErrUsernameTaken = errors.New("username already exists")
	ErrLastAdmin     = errors.New("at least one active administrator is required")
)

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(row userScanner) (model.Admin, error) {
	var (
		user                   model.Admin
		disabled               int
		createdAtMS, updatedMS int64
	)
	if err := row.Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.Role,
		&disabled, &createdAtMS, &updatedMS,
	); err != nil {
		return model.Admin{}, err
	}
	user.Disabled = disabled != 0
	user.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	user.UpdatedAt = time.UnixMilli(updatedMS).UTC()
	return user, nil
}

func validRole(role string) bool {
	return role == model.RoleAdmin || role == model.RoleViewer
}

func (s *Store) ListUsers(ctx context.Context) ([]model.Admin, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, password_hash, role, disabled, created_at_ms, updated_at_ms
		FROM admin ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []model.Admin
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string, now time.Time) (model.Admin, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return model.Admin{}, errors.New("username must not be empty")
	}
	if !validRole(role) {
		return model.Admin{}, fmt.Errorf("invalid role %q", role)
	}
	createdAt := now.UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO admin (username, password_hash, role, disabled, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, 0, ?, ?)
	`, username, passwordHash, role, createdAt.UnixMilli(), createdAt.UnixMilli())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return model.Admin{}, ErrUsernameTaken
		}
		return model.Admin{}, fmt.Errorf("insert user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Admin{}, fmt.Errorf("user id: %w", err)
	}
	return model.Admin{
		ID:           id,
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}, nil
}

func (s *Store) UpdateUserRole(ctx context.Context, id int64, role string, now time.Time) error {
	if !validRole(role) {
		return fmt.Errorf("invalid role %q", role)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin role update: %w", err)
	}
	defer tx.Rollback()

	currentRole, disabled, err := userRoleState(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if currentRole == model.RoleAdmin && !disabled && role != model.RoleAdmin {
		count, err := activeAdminCount(ctx, tx, id)
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin SET role = ?, updated_at_ms = ? WHERE id = ?
	`, role, now.UTC().UnixMilli(), id); err != nil {
		return fmt.Errorf("update user role: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit role update: %w", err)
	}
	return nil
}

func (s *Store) SetUserDisabled(ctx context.Context, id int64, disabled bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin disable update: %w", err)
	}
	defer tx.Rollback()

	currentRole, currentDisabled, err := userRoleState(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if disabled && currentRole == model.RoleAdmin && !currentDisabled {
		count, err := activeAdminCount(ctx, tx, id)
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin SET disabled = ?, updated_at_ms = ? WHERE id = ?
	`, boolInt(disabled), now.UTC().UnixMilli(), id); err != nil {
		return fmt.Errorf("update user disabled state: %w", err)
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE admin_id = ?", id); err != nil {
			return fmt.Errorf("delete user sessions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit disable update: %w", err)
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user deletion: %w", err)
	}
	defer tx.Rollback()

	currentRole, currentDisabled, err := userRoleState(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if currentRole == model.RoleAdmin && !currentDisabled {
		count, err := activeAdminCount(ctx, tx, id)
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE admin_id = ?", id); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit user deletion: %w", err)
	}
	return nil
}

// ResetUserPassword sets a new hash, re-enables the account and invalidates all
// of its sessions. It is used by the recovery CLI and the user management UI.
func (s *Store) ResetUserPassword(ctx context.Context, id int64, passwordHash string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE admin
		SET password_hash = ?, disabled = 0, updated_at_ms = ?
		WHERE id = ?
	`, passwordHash, now.UTC().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("reset user password: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("reset user rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE admin_id = ?", id); err != nil {
		return fmt.Errorf("delete reset user sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

func userRoleState(ctx context.Context, tx *sql.Tx, id int64) (string, bool, error) {
	var (
		role     string
		disabled int
	)
	if err := tx.QueryRowContext(ctx, "SELECT role, disabled FROM admin WHERE id = ?", id).Scan(&role, &disabled); err != nil {
		return "", false, err
	}
	return role, disabled != 0, nil
}

func activeAdminCount(ctx context.Context, tx *sql.Tx, excludeID int64) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM admin
		WHERE role = ? AND disabled = 0 AND id <> ?
	`, model.RoleAdmin, excludeID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active administrators: %w", err)
	}
	return count, nil
}
