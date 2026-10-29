package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

func TestResetPasswordFromStdin(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "windwatch.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC()
	admin, err := st.CreateFirstAdmin(t.Context(), "admin", "old-hash", now)
	if err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	session := model.Session{
		TokenHash: secret.HashToken("old-session"), AdminID: admin.ID,
		CSRFToken: "csrf", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := st.CreateSession(t.Context(), session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	oldStdin := os.Stdin
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	if _, err := write.WriteString("new-password-123\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = write.Close()
	os.Stdin = read
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = read.Close()
	})

	if err := runResetPassword([]string{"--data-dir", dir, "--password-stdin"}); err != nil {
		t.Fatalf("runResetPassword: %v", err)
	}

	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	admin, err = st.AdminByUsername(t.Context(), "admin")
	if err != nil {
		t.Fatalf("AdminByUsername: %v", err)
	}
	verified, err := secret.VerifyPassword(admin.PasswordHash, "new-password-123")
	if err != nil || !verified {
		t.Fatalf("new password verified=%v err=%v", verified, err)
	}
	if _, err := st.SessionByTokenHash(t.Context(), secret.HashToken("old-session"), time.Now()); err == nil {
		t.Fatal("old session was not invalidated")
	}
}

func TestResetPasswordRequiresUsernameForMultipleUsers(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "windwatch.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.CreateFirstAdmin(t.Context(), "admin", "hash", now); err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	if _, err := st.CreateUser(t.Context(), "viewer", "hash", model.RoleViewer, now); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_ = st.Close()

	err = runResetPassword([]string{"--data-dir", dir, "--password-stdin"})
	if err == nil || !strings.Contains(err.Error(), "--username") {
		t.Fatalf("error = %v, want --username hint", err)
	}
}

func TestResetPasswordMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := runResetPassword([]string{"--data-dir", dir}); err == nil {
		t.Fatal("expected missing database error")
	}
}
