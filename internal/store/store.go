package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"

	"github.com/fynhq/windwatch/internal/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var (
	ErrNotFound    = errors.New("record not found")
	ErrAdminExists = errors.New("administrator already exists")
)

type Store struct {
	db *sql.DB
}

var (
	sqlitePragmas = []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	}
	sqliteHookOnce sync.Once
)

// registerSQLiteHook installs the pragmas on every physical connection. Setting
// them once after Open is not enough: database/sql may discard a connection and
// replace it without re-running connection-scoped pragmas.
func registerSQLiteHook() {
	sqliteHookOnce.Do(func() {
		sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
			for _, pragma := range sqlitePragmas {
				if _, err := conn.ExecContext(context.Background(), pragma, nil); err != nil {
					return fmt.Errorf("apply sqlite pragma %q: %w", pragma, err)
				}
			}
			return nil
		})
	})
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}

	registerSQLiteHook()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// Monitoring writes are serialized through a small number of goroutines;
	// one connection also makes per-connection pragmas predictable.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}
	if path != ":memory:" {
		_ = os.Chmod(path, 0o600)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

const disableForeignKeysDirective = "-- migrate:disable-foreign-keys"

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at_ms INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}

		var applied int
		err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if applied > 0 {
			continue
		}

		script, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if err := s.applyMigration(ctx, version, string(script)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, version int, script string) error {
	disableForeignKeys := strings.HasPrefix(strings.TrimSpace(script), disableForeignKeysDirective)
	if disableForeignKeys {
		if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("disable foreign keys: %w", err)
		}
		defer func() {
			_, _ = s.db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON")
		}()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if disableForeignKeys {
		if err := checkForeignKeys(ctx, tx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, applied_at_ms) VALUES (?, ?)",
		version, time.Now().UTC().UnixMilli(),
	); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID int64
		var parent string
		var foreignKeyID int64
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return fmt.Errorf("read foreign key violation: %w", err)
		}
		return fmt.Errorf("foreign key violation in %s row %d referencing %s", table, rowID, parent)
	}
	return rows.Err()
}

func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin").Scan(&count); err != nil {
		return false, fmt.Errorf("count administrators: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CreateFirstAdmin(ctx context.Context, username, passwordHash string, now time.Time) (model.Admin, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Admin{}, fmt.Errorf("begin admin creation: %w", err)
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin").Scan(&count); err != nil {
		return model.Admin{}, fmt.Errorf("count administrators: %w", err)
	}
	if count > 0 {
		return model.Admin{}, ErrAdminExists
	}

	createdAt := now.UTC()
	result, err := tx.ExecContext(ctx,
		"INSERT INTO admin (username, password_hash, role, disabled, created_at_ms, updated_at_ms) VALUES (?, ?, ?, 0, ?, ?)",
		username, passwordHash, model.RoleAdmin, createdAt.UnixMilli(), createdAt.UnixMilli(),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return model.Admin{}, ErrAdminExists
		}
		return model.Admin{}, fmt.Errorf("insert administrator: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Admin{}, fmt.Errorf("administrator id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Admin{}, fmt.Errorf("commit administrator: %w", err)
	}
	return model.Admin{
		ID:           id,
		Username:     username,
		PasswordHash: passwordHash,
		Role:         model.RoleAdmin,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}, nil
}

func (s *Store) AdminByUsername(ctx context.Context, username string) (model.Admin, error) {
	var (
		admin                  model.Admin
		disabled               int
		createdAtMS, updatedMS int64
	)
	err := s.db.QueryRowContext(ctx,
		"SELECT id, username, password_hash, role, disabled, created_at_ms, updated_at_ms FROM admin WHERE username = ?",
		username,
	).Scan(&admin.ID, &admin.Username, &admin.PasswordHash, &admin.Role, &disabled, &createdAtMS, &updatedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Admin{}, ErrNotFound
	}
	if err != nil {
		return model.Admin{}, fmt.Errorf("get user: %w", err)
	}
	admin.Disabled = disabled != 0
	admin.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	admin.UpdatedAt = time.UnixMilli(updatedMS).UTC()
	return admin, nil
}

func (s *Store) CreateSession(ctx context.Context, session model.Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, admin_id, csrf_token, created_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?)
	`,
		session.TokenHash,
		session.AdminID,
		session.CSRFToken,
		session.CreatedAt.UTC().UnixMilli(),
		session.ExpiresAt.UTC().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string, now time.Time) (model.AuthInfo, error) {
	var (
		session                      model.Session
		admin                        model.Admin
		createdAtMS                  int64
		expiresAtMS                  int64
		adminDisabled                int
		adminCreatedAtMS, adminUpdMS int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.token_hash, s.admin_id, s.csrf_token, s.created_at_ms, s.expires_at_ms,
		       a.id, a.username, a.password_hash, a.role, a.disabled, a.created_at_ms, a.updated_at_ms
		FROM sessions s
		JOIN admin a ON a.id = s.admin_id
		WHERE s.token_hash = ? AND s.expires_at_ms > ? AND a.disabled = 0
	`, tokenHash, now.UTC().UnixMilli()).Scan(
		&session.TokenHash,
		&session.AdminID,
		&session.CSRFToken,
		&createdAtMS,
		&expiresAtMS,
		&admin.ID,
		&admin.Username,
		&admin.PasswordHash,
		&admin.Role,
		&adminDisabled,
		&adminCreatedAtMS,
		&adminUpdMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AuthInfo{}, ErrNotFound
	}
	if err != nil {
		return model.AuthInfo{}, fmt.Errorf("get session: %w", err)
	}

	session.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	session.ExpiresAt = time.UnixMilli(expiresAtMS).UTC()
	admin.Disabled = adminDisabled != 0
	admin.CreatedAt = time.UnixMilli(adminCreatedAtMS).UTC()
	admin.UpdatedAt = time.UnixMilli(adminUpdMS).UTC()

	return model.AuthInfo{Admin: admin, Session: session}, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at_ms <= ?", now.UTC().UnixMilli()); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
