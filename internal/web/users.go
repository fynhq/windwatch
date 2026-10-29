package web

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

type userFormValues struct {
	Username string
	Role     string
}

type userRow struct {
	ID        int64
	Username  string
	Role      string
	RoleLabel string
	Disabled  bool
	IsSelf    bool
	Created   string
}

type roleOption struct {
	Value string
	Label string
}

type usersPage struct {
	pageData
	Users               []userRow
	Roles               []roleOption
	Values              userFormValues
	CreateUserOpen      bool
	PasswordResetUserID int64
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	page, err := s.loadUsersPage(r, auth, userFormValues{Role: model.RoleViewer})
	if err != nil {
		s.log.Error("load users", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	s.render(w, "users", http.StatusOK, page)
}

func (s *Server) loadUsersPage(r *http.Request, auth model.AuthInfo, values userFormValues) (usersPage, error) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		return usersPage{}, err
	}
	data := s.authenticatedPage(r, auth, "users.title")
	data.ActiveNav = "users"
	rows := make([]userRow, 0, len(users))
	for _, user := range users {
		rows = append(rows, userRow{
			ID:        user.ID,
			Username:  user.Username,
			Role:      user.Role,
			RoleLabel: s.roleLabel(data.I18n, user.Role),
			Disabled:  user.Disabled,
			IsSelf:    user.ID == auth.Admin.ID,
			Created:   formatTime(data.I18n, user.CreatedAt, data.Location),
		})
	}
	if values.Role == "" {
		values.Role = model.RoleViewer
	}
	return usersPage{
		pageData: data,
		Users:    rows,
		Roles: []roleOption{
			{Value: model.RoleAdmin, Label: data.I18n.T("users.roleAdmin")},
			{Value: model.RoleViewer, Label: data.I18n.T("users.roleViewer")},
		},
		Values: values,
	}, nil
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	values := userFormValues{
		Username: strings.TrimSpace(r.PostFormValue("username")),
		Role:     strings.TrimSpace(r.PostFormValue("role")),
	}
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")

	if key := validateUsername(values.Username); key != "" {
		s.renderUsersError(w, r, auth, http.StatusBadRequest, values, key)
		return
	}
	if key := validateNewPassword(password, confirm); key != "" {
		s.renderUsersError(w, r, auth, http.StatusBadRequest, values, key)
		return
	}
	if values.Role != model.RoleAdmin && values.Role != model.RoleViewer {
		s.renderUsersError(w, r, auth, http.StatusBadRequest, values, "error.invalidRole")
		return
	}

	hash, err := secret.HashPassword(password)
	if err != nil {
		s.log.Error("hash new user password", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if _, err := s.store.CreateUser(r.Context(), values.Username, hash, values.Role, s.now()); err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			s.renderUsersError(w, r, auth, http.StatusBadRequest, values, "error.usernameTaken")
			return
		}
		s.log.Error("create user", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/users?notice=user_created", http.StatusSeeOther)
}

func (s *Server) handleUserRole(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
		return
	}
	if id == auth.Admin.ID {
		s.renderUsersError(w, r, auth, http.StatusBadRequest, userFormValues{}, "error.cannotModifySelf")
		return
	}
	role := strings.TrimSpace(r.PostFormValue("role"))
	if err := s.store.UpdateUserRole(r.Context(), id, role, s.now()); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
		case errors.Is(err, store.ErrLastAdmin):
			s.renderUsersError(w, r, auth, http.StatusBadRequest, userFormValues{}, "error.lastAdmin")
		case strings.Contains(err.Error(), "invalid role"):
			s.renderUsersError(w, r, auth, http.StatusBadRequest, userFormValues{}, "error.invalidRole")
		default:
			s.log.Error("update user role", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		}
		return
	}
	http.Redirect(w, r, "/settings/users?notice=user_updated", http.StatusSeeOther)
}

func (s *Server) handleUserDisabled(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
		return
	}
	disabled := r.PostFormValue("disabled") == "1"
	if disabled && id == auth.Admin.ID {
		s.renderUsersError(w, r, auth, http.StatusBadRequest, userFormValues{}, "error.cannotModifySelf")
		return
	}
	if err := s.store.SetUserDisabled(r.Context(), id, disabled, s.now()); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
		case errors.Is(err, store.ErrLastAdmin):
			s.renderUsersError(w, r, auth, http.StatusBadRequest, userFormValues{}, "error.lastAdmin")
		default:
			s.log.Error("set user disabled", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		}
		return
	}
	notice := "user_enabled"
	if disabled {
		notice = "user_disabled"
	}
	http.Redirect(w, r, "/settings/users?notice="+notice, http.StatusSeeOther)
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
		return
	}
	if id == auth.Admin.ID {
		s.renderUsersError(w, r, auth, http.StatusBadRequest, userFormValues{}, "error.cannotModifySelf")
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
		case errors.Is(err, store.ErrLastAdmin):
			s.renderUsersError(w, r, auth, http.StatusBadRequest, userFormValues{}, "error.lastAdmin")
		default:
			s.log.Error("delete user", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		}
		return
	}
	http.Redirect(w, r, "/settings/users?notice=user_deleted", http.StatusSeeOther)
}

func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
		return
	}
	password := r.PostFormValue("password")
	confirm := r.PostFormValue("password_confirm")
	if key := validateNewPassword(password, confirm); key != "" {
		s.renderUsersPasswordError(w, r, auth, id, key)
		return
	}
	hash, err := secret.HashPassword(password)
	if err != nil {
		s.log.Error("hash reset password", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if err := s.store.ResetUserPassword(r.Context(), id, hash, s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, r, http.StatusNotFound, "error.userNotFound")
			return
		}
		s.log.Error("reset user password", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	if id == auth.Admin.ID {
		s.clearSessionCookie(w, r)
		http.Redirect(w, r, "/login?notice=password_reset", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/users?notice=user_password_reset", http.StatusSeeOther)
}

func (s *Server) renderUsersError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, status int, values userFormValues, key string) {
	page, err := s.loadUsersPage(r, auth, values)
	if err != nil {
		s.log.Error("reload users", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.Error = page.I18n.T(key)
	page.CreateUserOpen = r.Method == http.MethodPost && r.URL.Path == "/settings/users"
	s.render(w, "users", status, page)
}

func (s *Server) renderUsersPasswordError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, userID int64, key string) {
	page, err := s.loadUsersPage(r, auth, userFormValues{})
	if err != nil {
		s.log.Error("reload users", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.Error = page.I18n.T(key)
	page.PasswordResetUserID = userID
	s.render(w, "users", http.StatusBadRequest, page)
}

func (s *Server) roleLabel(translator interface{ T(string) string }, role string) string {
	switch role {
	case model.RoleAdmin:
		return translator.T("users.roleAdmin")
	case model.RoleViewer:
		return translator.T("users.roleViewer")
	default:
		return role
	}
}

func validateUsername(username string) string {
	switch {
	case utf8.RuneCountInString(username) < 3:
		return "error.usernameShort"
	case utf8.RuneCountInString(username) > 64:
		return "error.usernameLong"
	default:
		return ""
	}
}

func validateNewPassword(password, confirm string) string {
	switch {
	case utf8.RuneCountInString(password) < 10:
		return "error.passwordShort"
	case utf8.RuneCountInString(password) > 256:
		return "error.passwordLong"
	case password != confirm:
		return "error.passwordMismatch"
	default:
		return ""
	}
}
