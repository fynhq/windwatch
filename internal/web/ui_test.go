package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
)

func TestSettingsPagesUseTopNavigation(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client, _ := setupAdminSession(t, testServer)

	pages := []struct {
		path   string
		marker string
	}{
		{"/settings/general", `action="/settings/timezone"`},
		{"/settings/public", `action="/settings/public-status"`},
		{"/settings/notifications", `id="notification-channel"`},
		{"/settings/logs", `settings-section`},
		{"/settings/proxies", `action="/settings/proxies"`},
	}
	for _, page := range pages {
		response, err := client.Get(testServer.URL + page.path)
		if err != nil {
			t.Fatalf("GET %s: %v", page.path, err)
		}
		body := readResponseBody(t, response)
		if response.StatusCode != http.StatusOK || !strings.Contains(body, "<h1>Settings</h1>") || !strings.Contains(body, page.marker) {
			t.Fatalf("GET %s status=%d body=%s", page.path, response.StatusCode, body)
		}
		for _, want := range []string{`class="topbar"`, `class="mainnav"`, `class="subnav"`, `href="/settings/general"`, `href="/settings/proxies"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s missing top navigation %q", page.path, want)
			}
		}
		if strings.Contains(body, `class="sidebar`) || strings.Contains(body, `class="mobile-dock"`) {
			t.Fatalf("GET %s still renders the legacy sidebar", page.path)
		}
	}

	response, err := client.Get(testServer.URL + "/settings/general")
	if err != nil {
		t.Fatalf("GET general settings: %v", err)
	}
	body := readResponseBody(t, response)
	if strings.Contains(body, `id="notification-channel"`) || strings.Contains(body, `action="/settings/public-status"`) {
		t.Fatal("general settings page rendered unrelated sections")
	}

	response, err = client.Get(testServer.URL + "/settings/notifications")
	if err != nil {
		t.Fatalf("GET notification settings: %v", err)
	}
	body = readResponseBody(t, response)
	if strings.Contains(body, `action="/settings/timezone"`) || strings.Contains(body, `action="/settings/public-status"`) {
		t.Fatal("notification settings page rendered unrelated sections")
	}

	response, err = client.Get(testServer.URL + "/settings")
	if err != nil {
		t.Fatalf("GET /settings: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || response.Request.URL.Path != "/settings/general" {
		t.Fatalf("/settings status=%d path=%q", response.StatusCode, response.Request.URL.Path)
	}
	if !strings.Contains(body, `action="/settings/timezone"`) || strings.Contains(body, `class="settings-index"`) {
		t.Fatalf("settings landing did not resolve to the general section: %s", body)
	}
}

func TestTopbarBrandLanguageAndFooter(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client, _ := setupAdminSession(t, testServer)

	response, err := client.Get(testServer.URL + "/settings/general")
	if err != nil {
		t.Fatalf("GET settings: %v", err)
	}
	body := readResponseBody(t, response)
	for _, want := range []string{
		"WindWatch Console",
		`class="topbar"`,
		`class="brand-mark"`,
		`class="mainnav"`,
		`class="subnav"`,
		`class="site-footer"`,
		`href="/settings/general"`,
		`href="/settings/users"`,
		`https://github.com/fynhq/windwatch`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings page missing %q", want)
		}
	}
	for _, old := range []string{`class="sidebar`, `class="mobile-dock"`, `class="mobile-topbar"`} {
		if strings.Contains(body, old) {
			t.Fatalf("settings page still renders legacy shell %q", old)
		}
	}
	if strings.Contains(body, "Hear the wind, hold the line.") {
		t.Fatal("authenticated topbar should not duplicate the auth tagline")
	}

	response, err = client.Get(testServer.URL + "/language/zh-CN?next=%2Fsettings%2Fgeneral")
	if err != nil {
		t.Fatalf("switch to Chinese: %v", err)
	}
	_ = readResponseBody(t, response)
	response, err = client.Get(testServer.URL + "/settings/general")
	if err != nil {
		t.Fatalf("GET settings in Chinese: %v", err)
	}
	body = readResponseBody(t, response)
	for _, want := range []string{"听风台", "设置", "时区"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Chinese settings page missing %q", want)
		}
	}
}

func TestFlashNoticeRendersAsToast(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client, _ := setupAdminSession(t, testServer)

	response, err := client.Get(testServer.URL + "/?notice=check_started")
	if err != nil {
		t.Fatalf("GET dashboard notice: %v", err)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, `class="toast toast-success"`) || !strings.Contains(body, "data-toast") {
		t.Fatalf("notice toast missing: %s", body)
	}
	if strings.Contains(body, `class="alert alert-success"`) {
		t.Fatalf("notice still rendered as a top alert: %s", body)
	}
}

func TestStaticAssetsAreVersioned(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client := newTestClient(t)

	response, err := client.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	body := readResponseBody(t, response)
	match := regexp.MustCompile(`/static/app\.css\?v=([0-9a-f]{12})`).FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("stylesheet URL is not versioned: %s", body)
	}
	for _, asset := range []string{"app.css", "pages.css", "app.js", "windwatch-icon.svg"} {
		response, err := client.Get(testServer.URL + "/static/" + asset + "?v=" + match[1])
		if err != nil {
			t.Fatalf("GET %s: %v", asset, err)
		}
		_ = readResponseBody(t, response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", asset, response.StatusCode)
		}
	}
}

func TestUsersCreateDialog(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client, _ := setupAdminSession(t, testServer)

	response, err := client.Get(testServer.URL + "/settings/users")
	if err != nil {
		t.Fatalf("GET users: %v", err)
	}
	body := readResponseBody(t, response)
	for _, want := range []string{
		`data-dialog-open="create-user"`,
		`<dialog class="dialog" id="create-user"`,
		`class="dialog-form"`,
		`data-dialog-close`,
		`data-dialog-open="reset-password-`,
		`<dialog class="dialog" id="reset-password-`,
		`Reset password`,
		`class="topbar"`,
		`class="mainnav"`,
		`class="site-footer"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("users page missing %q", want)
		}
	}
	if strings.Contains(body, `action="/settings/users" class="stack"`) {
		t.Fatal("create user form is still rendered inline at the top of the page")
	}
	if strings.Contains(body, `class="sidebar`) || strings.Contains(body, `class="mobile-dock"`) {
		t.Fatal("users page still renders the legacy sidebar or dock")
	}
}

func TestUsersRoleUpdate(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)
	hash, err := secret.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := st.CreateUser(t.Context(), "operator", hash, model.RoleViewer, time.Now())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	rolePath := "/settings/users/" + strconv.FormatInt(user.ID, 10) + "/role"

	response, err := client.PostForm(testServer.URL+rolePath, url.Values{
		"csrf_token": {csrf},
		"role":       {model.RoleAdmin},
	})
	if err != nil {
		t.Fatalf("POST role: %v", err)
	}
	body := readResponseBody(t, response)
	if response.Request.URL.Path != "/settings/users" {
		t.Fatalf("role update ended at %q, want /settings/users", response.Request.URL.Path)
	}
	updated, err := st.AdminByUsername(t.Context(), "operator")
	if err != nil {
		t.Fatalf("AdminByUsername: %v", err)
	}
	if updated.Role != model.RoleAdmin {
		t.Fatalf("role=%q, want %q", updated.Role, model.RoleAdmin)
	}
	for _, want := range []string{
		`action="` + rolePath + `" class="role-form" data-auto-submit`,
		`data-confirm="Delete this user?`,
		`class="text-button is-danger" type="submit">Delete</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("users page missing %q", want)
		}
	}
	if strings.Contains(body, "inline-details") {
		t.Fatal("delete action still uses an inline details toggle")
	}
}

func TestUsersResetPasswordDialogReopensOnError(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)
	hash, err := secret.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	userID, err := st.CreateUser(t.Context(), "viewer", hash, model.RoleViewer, time.Now())
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	response, err := client.PostForm(testServer.URL+"/settings/users/"+strconv.FormatInt(userID.ID, 10)+"/password", url.Values{
		"csrf_token":       {csrf},
		"password":         {"short"},
		"password_confirm": {"short"},
	})
	if err != nil {
		t.Fatalf("POST reset password: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("reset password status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, `data-auto-open="true"`) || !strings.Contains(body, "Password must be at least 10 characters.") {
		t.Fatalf("reset password dialog did not reopen: %s", body)
	}
}

func TestUsersCreateErrorReopensDialog(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.PostForm(testServer.URL+"/settings/users", url.Values{
		"csrf_token":       {csrf},
		"username":         {"ab"},
		"role":             {model.RoleViewer},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if err != nil {
		t.Fatalf("POST invalid user: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid user status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, `data-auto-open="true"`) {
		t.Fatal("create user dialog did not reopen after a validation error")
	}
	if !strings.Contains(body, "Username must be at least 3 characters.") {
		t.Fatalf("validation error missing: %s", body)
	}
}

func TestEventTableUsesDetailDialog(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	client, _ := setupAdminSession(t, testServer)
	id := createHTTPMonitor(t, srv, "Event source", "https://example.com/health")

	now := time.Now().UTC()
	message := "connection refused while checking https://example.com/health"
	if err := srv.store.RecordCheck(t.Context(), model.Check{
		MonitorID: id, CheckedAt: now, Status: model.CheckDown,
		ErrorKind: model.ErrorKindConnectionRefused, ErrorMessage: message, MetaJSON: "{}",
	}, model.MonitorState{
		Status: model.StatusDown, StatusSince: now, LastCheck: now, UpdatedAt: now,
	}, &model.Event{
		MonitorID: id, Type: model.EventDown, Message: message, StartedAt: now,
	}, false); err != nil {
		t.Fatalf("RecordCheck with event: %v", err)
	}

	response, err := client.Get(testServer.URL + "/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	body := readResponseBody(t, response)
	for _, want := range []string{
		`class="responsive-table events-table"`,
		`data-event-detail`,
		`data-event-message=`,
		`class="event-message-preview"`,
		`data-event-field="message"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("events page missing %q", want)
		}
	}
	if !strings.Contains(body, "View details") {
		t.Fatalf("events page missing detail action: %s", body)
	}

	response, err = client.Get(testServer.URL + "/monitors/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatalf("GET monitor detail: %v", err)
	}
	body = readResponseBody(t, response)
	if strings.Contains(body, "<h2>Recent checks</h2>") {
		t.Fatal("monitor detail still renders the recent checks table")
	}
	if !strings.Contains(body, "<h2>Status history</h2>") {
		t.Fatal("monitor detail lost the status history panel")
	}
	if !strings.Contains(body, `class="page-back"`) {
		t.Fatal("monitor detail missing back link")
	}
}
