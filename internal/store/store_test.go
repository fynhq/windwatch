package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "nested", "windwatch.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

func TestMigrateAndAdminLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	hasAdmin, err := st.HasAdmin(ctx)
	if err != nil {
		t.Fatalf("HasAdmin: %v", err)
	}
	if hasAdmin {
		t.Fatal("expected a fresh database to have no admin")
	}

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	admin, err := st.CreateFirstAdmin(ctx, "admin", "hash", now)
	if err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	if admin.ID != 1 || admin.Username != "admin" {
		t.Fatalf("unexpected admin: %+v", admin)
	}
	if !admin.CreatedAt.Equal(now) {
		t.Fatalf("admin created_at = %s, want %s", admin.CreatedAt, now)
	}

	if _, err := st.CreateFirstAdmin(ctx, "other", "hash", now); !errors.Is(err, ErrAdminExists) {
		t.Fatalf("second CreateFirstAdmin error = %v, want ErrAdminExists", err)
	}

	stored, err := st.AdminByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("AdminByUsername: %v", err)
	}
	if stored.PasswordHash != "hash" {
		t.Fatalf("PasswordHash = %q", stored.PasswordHash)
	}
	if stored.CreatedAt.UnixMilli() != now.UnixMilli() {
		t.Fatalf("CreatedAt = %s", stored.CreatedAt)
	}

	if _, err := st.AdminByUsername(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing admin error = %v, want ErrNotFound", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if _, err := st.CreateFirstAdmin(ctx, "admin", "hash", now); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}

	session := model.Session{
		TokenHash: "token-hash",
		AdminID:   1,
		CSRFToken: "csrf",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	authInfo, err := st.SessionByTokenHash(ctx, "token-hash", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("SessionByTokenHash: %v", err)
	}
	if authInfo.Admin.Username != "admin" || authInfo.Session.CSRFToken != "csrf" {
		t.Fatalf("unexpected auth info: %+v", authInfo)
	}

	if _, err := st.SessionByTokenHash(ctx, "token-hash", now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session error = %v, want ErrNotFound", err)
	}

	if err := st.DeleteSession(ctx, "token-hash"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.SessionByTokenHash(ctx, "token-hash", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session error = %v, want ErrNotFound", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if _, err := st.CreateFirstAdmin(ctx, "admin", "hash", now); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	for _, token := range []string{"expired", "active"} {
		expires := now.Add(-time.Minute)
		if token == "active" {
			expires = now.Add(time.Hour)
		}
		if err := st.CreateSession(ctx, model.Session{
			TokenHash: token,
			AdminID:   1,
			CSRFToken: "csrf",
			CreatedAt: now,
			ExpiresAt: expires,
		}); err != nil {
			t.Fatalf("CreateSession %s: %v", token, err)
		}
	}

	if err := st.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if _, err := st.SessionByTokenHash(ctx, "expired", now.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session error = %v, want ErrNotFound", err)
	}
	if _, err := st.SessionByTokenHash(ctx, "active", now.Add(time.Minute)); err != nil {
		t.Fatalf("active session was removed: %v", err)
	}
}
