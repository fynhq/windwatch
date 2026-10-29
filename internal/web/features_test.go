package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
)

func loginAs(t *testing.T, testServerURL, username, password string) (*http.Client, string) {
	t.Helper()
	client := newTestClient(t)
	response, err := client.Get(testServerURL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	_ = readResponseBody(t, response)
	csrf := cookieValue(t, client, testServerURL+"/login", csrfCookieName)
	response, err = client.PostForm(testServerURL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {username},
		"password":   {password},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || response.Request.URL.Path != "/" {
		t.Fatalf("login status=%d path=%q body=%s", response.StatusCode, response.Request.URL.Path, body)
	}
	return client, extractCSRF(t, body)
}

func createHTTPMonitor(t *testing.T, srv *Server, name, target string) int64 {
	t.Helper()
	id, err := srv.store.CreateMonitor(t.Context(), model.Monitor{
		Name: name, Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: target, Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true, NotifyEnabled: true,
	}, time.Now())
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	return id
}

func TestViewerPermissions(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	adminClient, adminCSRF := setupAdminSession(t, testServer)

	hash, err := secret.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := st.CreateUser(t.Context(), "viewer", hash, model.RoleViewer, time.Now()); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	response, err := adminClient.Get(testServer.URL + "/settings/users")
	if err != nil {
		t.Fatalf("admin GET users: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Create user") || !strings.Contains(body, "viewer") {
		t.Fatalf("admin users status=%d body=%s", response.StatusCode, body)
	}

	response, err = adminClient.PostForm(testServer.URL+"/settings/users", url.Values{
		"csrf_token":       {adminCSRF},
		"username":         {"operator"},
		"role":             {model.RoleViewer},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_ = readResponseBody(t, response)
	if _, err := st.AdminByUsername(t.Context(), "operator"); err != nil {
		t.Fatalf("operator was not created: %v", err)
	}

	viewerClient, _ := loginAs(t, testServer.URL, "viewer", testPassword)
	for _, path := range []string{"/", "/events"} {
		response, err = viewerClient.Get(testServer.URL + path)
		if err != nil {
			t.Fatalf("viewer GET %s: %v", path, err)
		}
		_ = readResponseBody(t, response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("viewer GET %s status=%d", path, response.StatusCode)
		}
	}
	for _, path := range []string{"/settings", "/settings/users", "/monitors/new"} {
		response, err = viewerClient.Get(testServer.URL + path)
		if err != nil {
			t.Fatalf("viewer GET %s: %v", path, err)
		}
		_ = readResponseBody(t, response)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("viewer GET %s status=%d, want 403", path, response.StatusCode)
		}
	}

	id := createHTTPMonitor(t, srv, "Internal API", "https://internal.example.com/health")
	response, err = adminClient.Get(testServer.URL + "/monitors/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatalf("admin monitor detail: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Check now") {
		t.Fatalf("admin detail status=%d body=%s", response.StatusCode, body)
	}

	response, err = viewerClient.Get(testServer.URL + "/monitors/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatalf("viewer monitor detail: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || strings.Contains(body, "Check now") || strings.Contains(body, "Danger zone") {
		t.Fatalf("viewer detail status=%d body=%s", response.StatusCode, body)
	}
}

func TestPublicStatusPage(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	adminClient, csrf := setupAdminSession(t, testServer)

	publicID := createHTTPMonitor(t, srv, "Public site", "https://secret.example.com/health")
	_ = createHTTPMonitor(t, srv, "Private site", "https://private.example.com/health")

	response, err := adminClient.PostForm(testServer.URL+"/settings/public-status", url.Values{
		"csrf_token":                {csrf},
		"public_status_enabled":     {"1"},
		"public_status_title":       {"Acme status"},
		"public_status_description": {"Live service health"},
		"public_monitors":           {strconv.FormatInt(publicID, 10)},
	})
	if err != nil {
		t.Fatalf("save public status: %v", err)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "Public status page settings saved.") {
		t.Fatalf("save public status missing notice: %s", body)
	}
	if !strings.Contains(body, "data-copy-value") || !strings.Contains(body, testServer.URL+"/status") {
		t.Fatalf("public status address is not copyable: %s", body)
	}

	anonymous := newTestClient(t)
	response, err = anonymous.Get(testServer.URL + "/status")
	if err != nil {
		t.Fatalf("GET /status: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/status status=%d body=%s", response.StatusCode, body)
	}
	for _, want := range []string{"Acme status", "Live service health", "Public site", "public-history-bars", "90d uptime", "public-history-range-narrow"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/status missing %q: %s", want, body)
		}
	}
	if got := response.Header.Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Fatalf("/status robots header = %q", got)
	}
	if strings.Contains(body, "Private site") || strings.Contains(body, "secret.example.com") || strings.Contains(body, "private.example.com") {
		t.Fatalf("/status leaked private data: %s", body)
	}
	if strings.Contains(body, "Check now") {
		t.Fatal("/status exposed admin actions")
	}
	if !strings.Contains(body, `href="https://github.com/fynhq/windwatch"`) || !strings.Contains(body, "WindWatch test") {
		t.Fatalf("/status missing version link: %s", body)
	}
	if strings.Contains(body, `class="app-footer"`) {
		t.Fatalf("/status still renders the page footer: %s", body)
	}

	response, err = adminClient.PostForm(testServer.URL+"/settings/public-status", url.Values{
		"csrf_token":      {csrf},
		"public_monitors": {strconv.FormatInt(publicID, 10)},
	})
	if err != nil {
		t.Fatalf("disable public status: %v", err)
	}
	_ = readResponseBody(t, response)
	response, err = anonymous.Get(testServer.URL + "/status")
	if err != nil {
		t.Fatalf("GET disabled /status: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled /status status=%d, want 404", response.StatusCode)
	}
}

func TestDashboardRecentChecksAndHistoryTabs(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	adminClient, _ := setupAdminSession(t, testServer)
	id := createHTTPMonitor(t, srv, "Recent checks", "https://example.com/health")

	start := time.Now().UTC().Add(-3 * time.Minute)
	statuses := []string{model.CheckUp, model.CheckDown, model.CheckUp}
	for index, status := range statuses {
		checkedAt := start.Add(time.Duration(index) * 10 * time.Second)
		if err := srv.store.RecordCheck(t.Context(), model.Check{
			MonitorID: id, CheckedAt: checkedAt, Status: status,
			Latency: 20 * time.Millisecond, MetaJSON: "{}",
		}, model.MonitorState{
			Status: model.StatusUp, StatusSince: checkedAt, LastCheck: checkedAt, UpdatedAt: checkedAt,
		}, nil, false); err != nil {
			t.Fatalf("RecordCheck: %v", err)
		}
	}

	response, err := adminClient.Get(testServer.URL + "/")
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Last 24 checks") {
		t.Fatalf("dashboard recent checks label missing: status=%d body=%s", response.StatusCode, body)
	}
	for _, expected := range []string{`data-monitor-filter="down"`, `data-monitor-search`, "Current latency", "Last 24 checks:"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("dashboard missing %q: %s", expected, body)
		}
	}
	bars := regexp.MustCompile(`<span class="(bar-[a-z]+)"`).FindAllStringSubmatch(body, -1)
	if len(bars) != 24 {
		t.Fatalf("dashboard bars = %d, want 24", len(bars))
	}
	want := []string{"bar-up", "bar-down", "bar-up"}
	for index := range want {
		if got := bars[len(bars)-len(want)+index][1]; got != want[index] {
			t.Fatalf("recent bar %d = %s, want %s", index, got, want[index])
		}
	}

	response, err = adminClient.Get(testServer.URL + "/monitors/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatalf("GET monitor detail: %v", err)
	}
	body = readResponseBody(t, response)
	for _, expected := range []string{
		`data-history-tabs`, `data-history-tab="history-24h"`, `id="history-30d"`,
		`id="history-90d"`, "Status history", "Last 24 checks",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("detail missing %q: %s", expected, body)
		}
	}
}

func TestPartialRefreshFragments(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	adminClient, csrf := setupAdminSession(t, testServer)
	id := createHTTPMonitor(t, srv, "Partial monitor", "https://secret.example.com/health")

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/", nil)
	if err != nil {
		t.Fatalf("new dashboard request: %v", err)
	}
	request.Header.Set(partialRefreshHeader, "dashboard")
	response, err := adminClient.Do(request)
	if err != nil {
		t.Fatalf("dashboard fragment: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("dashboard fragment status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, `data-live-region="dashboard"`) || !strings.Contains(body, "Partial monitor") {
		t.Fatalf("dashboard fragment missing content: %s", body)
	}
	if strings.Contains(body, "<!doctype html>") || strings.Contains(body, `class="sidebar`) {
		t.Fatalf("dashboard fragment returned a full page: %s", body)
	}
	if strings.Contains(body, "<noscript>") {
		t.Fatalf("dashboard fragment should not include the no-JS refresh meta: %s", body)
	}

	response, err = adminClient.Get(testServer.URL + "/")
	if err != nil {
		t.Fatalf("GET full dashboard: %v", err)
	}
	fullBody := readResponseBody(t, response)
	if !strings.Contains(fullBody, `<noscript><meta http-equiv="refresh" content="30"></noscript>`) {
		t.Fatalf("full dashboard missing no-JS refresh fallback: %s", fullBody)
	}

	response, err = adminClient.PostForm(testServer.URL+"/settings/public-status", url.Values{
		"csrf_token":                {csrf},
		"public_status_enabled":     {"1"},
		"public_status_title":       {"Partial status"},
		"public_status_description": {"Fragment test"},
		"public_monitors":           {strconv.FormatInt(id, 10)},
	})
	if err != nil {
		t.Fatalf("enable public status: %v", err)
	}
	_ = readResponseBody(t, response)

	anonymous := newTestClient(t)
	request, err = http.NewRequest(http.MethodGet, testServer.URL+"/status", nil)
	if err != nil {
		t.Fatalf("new status request: %v", err)
	}
	request.Header.Set(partialRefreshHeader, "status")
	response, err = anonymous.Do(request)
	if err != nil {
		t.Fatalf("status fragment: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status fragment status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, `data-live-region="status"`) || !strings.Contains(body, "Partial monitor") {
		t.Fatalf("status fragment missing content: %s", body)
	}
	if strings.Contains(body, "<!doctype html>") || strings.Contains(body, "secret.example.com") {
		t.Fatalf("status fragment returned a full page or leaked the target: %s", body)
	}
}

func TestMonitorFormLayout(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	adminClient, _ := setupAdminSession(t, testServer)
	id := createHTTPMonitor(t, srv, "Form layout", "https://example.com/health")

	response, err := adminClient.Get(testServer.URL + "/monitors/" + strconv.FormatInt(id, 10) + "/edit")
	if err != nil {
		t.Fatalf("GET edit form: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("edit form status=%d body=%s", response.StatusCode, body)
	}
	for _, expected := range []string{`id="monitor-form"`, `form="monitor-form"`, `<h2>Basics</h2>`, `<h2>HTTP request</h2>`, `<h2>Schedule and thresholds</h2>`, `<h2>Notifications</h2>`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("edit form missing %q: %s", expected, body)
		}
	}
	basic := strings.Index(body, "<h2>Basics</h2>")
	httpSection := strings.Index(body, "<h2>HTTP request</h2>")
	schedule := strings.Index(body, "<h2>Schedule and thresholds</h2>")
	notifications := strings.Index(body, "<h2>Notifications</h2>")
	if !(basic >= 0 && basic < httpSection && httpSection < schedule && schedule < notifications) {
		t.Fatalf("form section order = %d, %d, %d, %d", basic, httpSection, schedule, notifications)
	}
}

func TestLongRangeChartRendering(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	adminClient, _ := setupAdminSession(t, testServer)
	id := createHTTPMonitor(t, srv, "Daily chart", "https://example.com/health")

	now := time.Now().UTC().Truncate(24 * time.Hour)
	checkAt := now.Add(-24*time.Hour + 2*time.Hour)
	if err := srv.store.RecordCheck(t.Context(), model.Check{
		MonitorID: id, CheckedAt: checkAt, Status: model.CheckUp,
		Latency: 25 * time.Millisecond, MetaJSON: "{}",
	}, model.MonitorState{
		Status: model.StatusUp, StatusSince: checkAt, LastCheck: checkAt, UpdatedAt: checkAt,
	}, nil, false); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if err := srv.store.AggregateHalfHourlyRollups(t.Context(), now.Add(-48*time.Hour), now, time.Now()); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}

	response, err := adminClient.Get(testServer.URL + "/monitors/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatalf("GET monitor detail: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, "30d daily uptime") || !strings.Contains(body, "long-range-chart") || !strings.Contains(body, "bar-up") {
		t.Fatalf("long-range chart missing: %s", body)
	}
}

func TestTrustedProxyPushURL(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	adminClient, _ := setupAdminSession(t, testServer)

	id, err := srv.store.CreateMonitor(t.Context(), model.Monitor{
		Name: "Heartbeat", Type: model.MonitorTypePush,
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true, NotifyEnabled: true,
	}, time.Now())
	if err != nil {
		t.Fatalf("CreateMonitor push: %v", err)
	}
	plaintext := "test-push-token"
	encrypted, err := srv.cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := srv.store.CreatePushToken(t.Context(), id, secret.HashToken(plaintext), encrypted, time.Now()); err != nil {
		t.Fatalf("CreatePushToken: %v", err)
	}

	srv.trustedProxies = []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
	}
	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/monitors/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "status.example.com")
	response, err := adminClient.Do(request)
	if err != nil {
		t.Fatalf("GET push detail: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "https://status.example.com/api/push/"+plaintext) {
		t.Fatalf("push URL not rewritten status=%d body=%s", response.StatusCode, body)
	}
}

func TestAuthenticatedErrorPageKeepsLayout(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	adminClient, _ := setupAdminSession(t, testServer)
	response, err := adminClient.Get(testServer.URL + "/monitors/999999")
	if err != nil {
		t.Fatalf("GET missing monitor: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if !strings.Contains(body, `class="app-body"`) || !strings.Contains(body, `class="sidebar`) {
		t.Fatalf("error page lost authenticated layout: %s", body)
	}
}

func TestRequestLogPathRedactsPushToken(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.com/api/push/secret-token", nil)
	if got := requestLogPath(request); got != "/api/push/[redacted]" {
		t.Fatalf("requestLogPath = %q", got)
	}
	request = httptest.NewRequest("GET", "http://example.com/events", nil)
	if got := requestLogPath(request); got != "/events" {
		t.Fatalf("requestLogPath = %q", got)
	}
}
