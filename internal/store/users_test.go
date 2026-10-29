package store

import (
	"errors"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
)

func TestUserLifecycleAndLastAdminProtection(t *testing.T) {
	st := newStoreTest(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	admin, err := st.CreateFirstAdmin(ctx, "admin", "hash", now)
	if err != nil {
		t.Fatalf("CreateFirstAdmin: %v", err)
	}
	if admin.Role != model.RoleAdmin || admin.Disabled {
		t.Fatalf("first admin = %+v", admin)
	}
	if err := st.SetUserDisabled(ctx, admin.ID, true, now); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin error = %v, want ErrLastAdmin", err)
	}
	if err := st.UpdateUserRole(ctx, admin.ID, model.RoleViewer, now); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin error = %v, want ErrLastAdmin", err)
	}
	if err := st.DeleteUser(ctx, admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete last admin error = %v, want ErrLastAdmin", err)
	}

	viewer, err := st.CreateUser(ctx, "viewer", "hash", model.RoleViewer, now)
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}
	if _, err := st.CreateUser(ctx, "viewer", "hash", model.RoleViewer, now); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate user error = %v, want ErrUsernameTaken", err)
	}
	users, err := st.ListUsers(ctx)
	if err != nil || len(users) != 2 {
		t.Fatalf("ListUsers = %+v err=%v", users, err)
	}

	session := model.Session{
		TokenHash: secret.HashToken("viewer-token"), AdminID: viewer.ID,
		CSRFToken: "csrf", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.SessionByTokenHash(ctx, session.TokenHash, now); err != nil {
		t.Fatalf("viewer session: %v", err)
	}

	if err := st.SetUserDisabled(ctx, viewer.ID, true, now); err != nil {
		t.Fatalf("disable viewer: %v", err)
	}
	if _, err := st.SessionByTokenHash(ctx, session.TokenHash, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled viewer session error = %v, want ErrNotFound", err)
	}
	if err := st.ResetUserPassword(ctx, viewer.ID, "new-hash", now); err != nil {
		t.Fatalf("ResetUserPassword: %v", err)
	}
	reloaded, err := st.AdminByUsername(ctx, "viewer")
	if err != nil || reloaded.Disabled || reloaded.PasswordHash != "new-hash" {
		t.Fatalf("reset viewer = %+v err=%v", reloaded, err)
	}

	admin2, err := st.CreateUser(ctx, "admin2", "hash", model.RoleAdmin, now)
	if err != nil {
		t.Fatalf("CreateUser admin2: %v", err)
	}
	if err := st.SetUserDisabled(ctx, admin.ID, true, now); err != nil {
		t.Fatalf("disable first admin with another admin: %v", err)
	}
	if err := st.SetUserDisabled(ctx, admin2.ID, true, now); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last active admin error = %v, want ErrLastAdmin", err)
	}
	if err := st.DeleteUser(ctx, viewer.ID); err != nil {
		t.Fatalf("delete viewer: %v", err)
	}
	if _, err := st.AdminByUsername(ctx, "viewer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted user lookup error = %v, want ErrNotFound", err)
	}
}
