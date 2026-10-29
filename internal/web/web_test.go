package web

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/secret"
	"github.com/fynhq/windwatch/internal/store"
)

const testPassword = "correct-horse-battery"

type fakeScheduler struct {
	mu       sync.Mutex
	reloaded []int64
	removed  []int64
	checked  []int64
	checkErr error
}

func (f *fakeScheduler) Reload(monitorID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloaded = append(f.reloaded, monitorID)
	return nil
}

func (f *fakeScheduler) Remove(monitorID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, monitorID)
}

func (f *fakeScheduler) CheckNow(monitorID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked = append(f.checked, monitorID)
	return f.checkErr
}

type fakeNotifier struct {
	mu     sync.Mutex
	tested []int64
	err    error
}

func (f *fakeNotifier) Test(_ context.Context, channelID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, channelID)
	return f.err
}

func newWebTestServer(t *testing.T) (*Server, *store.Store, *httptest.Server) {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "windwatch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cipher, err := secret.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("secret.NewCipher: %v", err)
	}
	srv, err := New(st, catalogs, &fakeScheduler{}, &fakeNotifier{}, cipher, nil, logger, "test")
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	testServer := httptest.NewServer(srv.Handler())
	t.Cleanup(testServer.Close)
	return srv, st, testServer
}

func newTestClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

func readResponseBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}

func cookieValue(t *testing.T, client *http.Client, rawURL, name string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func extractCSRF(t *testing.T, body string) string {
	t.Helper()
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("csrf token not found in form")
	}
	return match[1]
}

func TestSetupLoginLogoutFlow(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client := newTestClient(t)

	response, err := client.Get(testServer.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /setup status = %d", response.StatusCode)
	}
	if !strings.Contains(body, "Create administrator") {
		t.Fatalf("setup page missing English copy: %s", body)
	}

	response, err = client.PostForm(testServer.URL+"/setup", url.Values{
		"username":         {"admin"},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if err != nil {
		t.Fatalf("POST /setup without CSRF: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /setup without CSRF status = %d, want 403", response.StatusCode)
	}
	if !strings.Contains(body, "session token is invalid") {
		t.Fatalf("missing CSRF error: %s", body)
	}

	csrf := cookieValue(t, client, testServer.URL+"/setup", csrfCookieName)
	if csrf == "" {
		t.Fatal("pre-auth CSRF cookie not set")
	}
	response, err = client.PostForm(testServer.URL+"/setup", url.Values{
		"csrf_token":       {csrf},
		"username":         {"admin"},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if err != nil {
		t.Fatalf("POST /setup: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || response.Request.URL.Path != "/" {
		t.Fatalf("setup redirect status=%d path=%q", response.StatusCode, response.Request.URL.Path)
	}
	if !strings.Contains(body, "Dashboard") || !strings.Contains(body, "admin") {
		t.Fatalf("dashboard missing expected content: %s", body)
	}
	if cookieValue(t, client, testServer.URL+"/", sessionCookieName) == "" {
		t.Fatal("session cookie not set")
	}
	hasAdmin, err := st.HasAdmin(t.Context())
	if err != nil {
		t.Fatalf("HasAdmin: %v", err)
	}
	if !hasAdmin {
		t.Fatal("administrator was not persisted")
	}

	response, err = client.Get(testServer.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup after setup: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusNotFound || !strings.Contains(body, "Setup is already complete") {
		t.Fatalf("setup after setup status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.Get(testServer.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d", response.StatusCode)
	}
	logoutCSRF := extractCSRF(t, body)

	response, err = client.PostForm(testServer.URL+"/logout", url.Values{"csrf_token": {logoutCSRF}})
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.Request.URL.Path != "/login" {
		t.Fatalf("logout final path = %q, want /login", response.Request.URL.Path)
	}
	if cookieValue(t, client, testServer.URL+"/", sessionCookieName) != "" {
		t.Fatal("session cookie still present after logout")
	}

	response, err = client.Get(testServer.URL + "/")
	if err != nil {
		t.Fatalf("GET / after logout: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.Request.URL.Path != "/login" {
		t.Fatalf("unauthenticated final path = %q, want /login", response.Request.URL.Path)
	}

	response, err = client.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Sign in") {
		t.Fatalf("login page status=%d body=%s", response.StatusCode, body)
	}
	csrf = cookieValue(t, client, testServer.URL+"/login", csrfCookieName)

	response, err = client.PostForm(testServer.URL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {"admin"},
		"password":   {testPassword},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || response.Request.URL.Path != "/" || !strings.Contains(body, "Dashboard") {
		t.Fatalf("login status=%d path=%q body=%s", response.StatusCode, response.Request.URL.Path, body)
	}
}

func TestLoginFailureAndLanguage(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client := newTestClient(t)

	response, err := client.Get(testServer.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	_ = readResponseBody(t, response)
	csrf := cookieValue(t, client, testServer.URL+"/setup", csrfCookieName)
	response, err = client.PostForm(testServer.URL+"/setup", url.Values{
		"csrf_token":       {csrf},
		"username":         {"admin"},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if err != nil {
		t.Fatalf("POST /setup: %v", err)
	}
	_ = readResponseBody(t, response)

	anonymous := newTestClient(t)
	response, err = anonymous.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	_ = readResponseBody(t, response)
	csrf = cookieValue(t, anonymous, testServer.URL+"/login", csrfCookieName)

	response, err = anonymous.PostForm(testServer.URL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {"admin"},
		"password":   {"wrong-password"},
	})
	if err != nil {
		t.Fatalf("POST wrong login: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "incorrect") {
		t.Fatalf("wrong login status=%d body=%s", response.StatusCode, body)
	}

	response, err = anonymous.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	_ = readResponseBody(t, response)

	request, err := http.NewRequest(http.MethodGet, testServer.URL+"/login", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Accept-Language", "zh-CN")
	response, err = anonymous.Do(request)
	if err != nil {
		t.Fatalf("GET login zh: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, `<html lang="zh-CN">`) || !strings.Contains(body, "登录") {
		t.Fatalf("Chinese login page not rendered: %s", body)
	}

	response, err = anonymous.Get(testServer.URL + "/language/en?next=/login")
	if err != nil {
		t.Fatalf("GET language switch: %v", err)
	}
	body = readResponseBody(t, response)
	if response.Request.URL.Path != "/login" || !strings.Contains(body, "Sign in") || !strings.Contains(body, `<html lang="en">`) {
		t.Fatalf("English switch failed path=%q body=%s", response.Request.URL.Path, body)
	}

	hasAdmin, err := st.HasAdmin(t.Context())
	if err != nil {
		t.Fatalf("HasAdmin: %v", err)
	}
	if !hasAdmin {
		t.Fatal("administrator missing")
	}
}

func TestHealthAndStatic(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client := newTestClient(t)

	response, err := client.Get(testServer.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("health status=%d body=%s", response.StatusCode, body)
	}
	if response.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("health content type = %q", response.Header.Get("Content-Type"))
	}

	response, err = client.Get(testServer.URL + "/static/app.css")
	if err != nil {
		t.Fatalf("GET static: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "--accent") {
		t.Fatalf("static status=%d", response.StatusCode)
	}
	if !strings.Contains(response.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("static content type = %q", response.Header.Get("Content-Type"))
	}

	response, err = client.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	body = readResponseBody(t, response)
	if response.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP header")
	}
	if !strings.Contains(body, `rel="icon"`) || !strings.Contains(body, "/static/windwatch-icon.svg") {
		t.Fatal("logo favicon missing from layout")
	}
	if strings.Contains(body, "data-theme-toggle") || strings.Contains(body, "/static/theme.js") {
		t.Fatal("theme toggle should not be rendered")
	}

	response, err = client.Get(testServer.URL + "/static/windwatch-icon.svg")
	if err != nil {
		t.Fatalf("GET logo: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "<svg") {
		t.Fatalf("logo status=%d", response.StatusCode)
	}
	if !strings.Contains(response.Header.Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("logo content type = %q", response.Header.Get("Content-Type"))
	}

	response, err = client.Get(testServer.URL + "/static/app.css")
	if err != nil {
		t.Fatalf("GET app.css: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "prefers-color-scheme") || strings.Contains(body, `data-theme="dark"`) {
		t.Fatal("CSS should follow the system color scheme without manual theme overrides")
	}
}

func TestSafeRedirectPath(t *testing.T) {
	valid := []string{"/", "/monitors/1", "/login?next=%2F"}
	for _, value := range valid {
		if !safeRedirectPath(value) {
			t.Errorf("safeRedirectPath(%q) = false, want true", value)
		}
	}
	invalid := []string{"", "https://example.com", "//example.com", `/\example.com`, "/line\nbreak"}
	for _, value := range invalid {
		if safeRedirectPath(value) {
			t.Errorf("safeRedirectPath(%q) = true, want false", value)
		}
	}
}

func TestLoginLimiter(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for i := 0; i < maxLoginFailures; i++ {
		limiter.failure("192.0.2.1", now.Add(time.Duration(i)*time.Second))
	}
	if !limiter.blocked("192.0.2.1", now.Add(time.Minute)) {
		t.Fatal("expected limiter to block after maximum failures")
	}
	if limiter.blocked("192.0.2.1", now.Add(6*time.Minute)) {
		t.Fatal("expected block to expire")
	}
	limiter.reset("192.0.2.1")
	if limiter.blocked("192.0.2.1", now) {
		t.Fatal("reset should clear the limiter")
	}
}

func TestMonitorCreateCheckToggleDelete(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	scheduler := srv.scheduler.(*fakeScheduler)
	client := newTestClient(t)

	response, err := client.Get(testServer.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	_ = readResponseBody(t, response)
	csrf := cookieValue(t, client, testServer.URL+"/setup", csrfCookieName)
	response, err = client.PostForm(testServer.URL+"/setup", url.Values{
		"csrf_token":       {csrf},
		"username":         {"admin"},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if err != nil {
		t.Fatalf("POST /setup: %v", err)
	}
	body := readResponseBody(t, response)
	sessionCSRF := extractCSRF(t, body)

	response, err = client.Get(testServer.URL + "/monitors/new")
	if err != nil {
		t.Fatalf("GET type picker: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Choose a monitor type") {
		t.Fatalf("type picker status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.Get(testServer.URL + "/monitors/new?type=http")
	if err != nil {
		t.Fatalf("GET HTTP form: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "HTTP request") {
		t.Fatalf("HTTP form status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/monitors", url.Values{
		"csrf_token":        {sessionCSRF},
		"type":              {"http"},
		"name":              {"Example site"},
		"url":               {"https://example.com"},
		"method":            {"GET"},
		"expected_status":   {"200-299"},
		"follow_redirects":  {"1"},
		"interval":          {"60"},
		"timeout":           {"10"},
		"fail_threshold":    {"2"},
		"recover_threshold": {"1"},
	})
	if err != nil {
		t.Fatalf("POST monitor: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Example site") {
		t.Fatalf("detail after create status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, "Uptime 30d") || !strings.Contains(body, "Uptime 90d") {
		t.Fatalf("detail missing long-range uptime cards: %s", body)
	}
	monitors, err := st.ListMonitors(t.Context())
	if err != nil || len(monitors) != 1 {
		t.Fatalf("monitors=%+v err=%v", monitors, err)
	}
	monitor := monitors[0]
	if len(scheduler.reloaded) != 1 || scheduler.reloaded[0] != monitor.ID {
		t.Fatalf("scheduler reloads = %+v", scheduler.reloaded)
	}

	response, err = client.Get(testServer.URL + "/")
	if err != nil {
		t.Fatalf("GET dashboard: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Example site") || !strings.Contains(body, "Uptime 24h") {
		t.Fatalf("dashboard missing monitor: %s", body)
	}

	response, err = client.PostForm(testServer.URL+"/monitors/"+strconv.FormatInt(monitor.ID, 10)+"/check", url.Values{
		"csrf_token": {sessionCSRF},
	})
	if err != nil {
		t.Fatalf("POST check: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Check requested") {
		t.Fatalf("check response status=%d body=%s", response.StatusCode, body)
	}
	if len(scheduler.checked) != 1 || scheduler.checked[0] != monitor.ID {
		t.Fatalf("scheduler checks = %+v", scheduler.checked)
	}

	response, err = client.PostForm(testServer.URL+"/monitors/"+strconv.FormatInt(monitor.ID, 10)+"/toggle", url.Values{
		"csrf_token": {sessionCSRF},
	})
	if err != nil {
		t.Fatalf("POST toggle: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Paused") || !strings.Contains(body, "Monitor paused") {
		t.Fatalf("toggle response missing paused state: %s", body)
	}
	paused, err := st.MonitorByID(t.Context(), monitor.ID)
	if err != nil || paused.Enabled {
		t.Fatalf("paused monitor=%+v err=%v", paused, err)
	}
	if strings.Contains(body, "Check now") {
		t.Fatalf("paused detail still exposes Check now: %s", body)
	}
	response, err = client.PostForm(testServer.URL+"/monitors/"+strconv.FormatInt(monitor.ID, 10)+"/check", url.Values{
		"csrf_token": {sessionCSRF},
	})
	if err != nil {
		t.Fatalf("POST paused check: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Resume this monitor before running a check.") {
		t.Fatalf("paused check notice missing: %s", body)
	}
	if len(scheduler.checked) != 1 {
		t.Fatalf("paused monitor invoked scheduler: %+v", scheduler.checked)
	}

	response, err = client.PostForm(testServer.URL+"/monitors/"+strconv.FormatInt(monitor.ID, 10)+"/delete", url.Values{
		"csrf_token": {sessionCSRF},
	})
	if err != nil {
		t.Fatalf("POST delete: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Monitor deleted") {
		t.Fatalf("delete response missing notice: %s", body)
	}
	if _, err := st.MonitorByID(t.Context(), monitor.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted monitor error = %v", err)
	}
	if len(scheduler.removed) != 1 || scheduler.removed[0] != monitor.ID {
		t.Fatalf("scheduler removals = %+v", scheduler.removed)
	}
}

func TestMonitorFormValidation(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client := newTestClient(t)

	response, err := client.Get(testServer.URL + "/setup")
	if err != nil {
		t.Fatalf("GET /setup: %v", err)
	}
	_ = readResponseBody(t, response)
	csrf := cookieValue(t, client, testServer.URL+"/setup", csrfCookieName)
	response, err = client.PostForm(testServer.URL+"/setup", url.Values{
		"csrf_token":       {csrf},
		"username":         {"admin"},
		"password":         {testPassword},
		"password_confirm": {testPassword},
	})
	if err != nil {
		t.Fatalf("POST /setup: %v", err)
	}
	body := readResponseBody(t, response)
	sessionCSRF := extractCSRF(t, body)

	response, err = client.PostForm(testServer.URL+"/monitors", url.Values{
		"csrf_token":        {sessionCSRF},
		"type":              {"tcp"},
		"name":              {"Bad port"},
		"host":              {"127.0.0.1"},
		"port":              {"70000"},
		"interval":          {"10"},
		"timeout":           {"10"},
		"fail_threshold":    {"2"},
		"recover_threshold": {"1"},
	})
	if err != nil {
		t.Fatalf("POST invalid monitor: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Interval must be between") {
		t.Fatalf("validation status=%d body=%s", response.StatusCode, body)
	}
}

func TestHSTSSetOnlyForHTTPS(t *testing.T) {
	srv, _, _ := newWebTestServer(t)
	handler := srv.Handler()

	request := httptest.NewRequest(http.MethodGet, "https://example.com/healthz", nil)
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HTTPS response is missing HSTS")
	}

	request = httptest.NewRequest(http.MethodGet, "http://example.com/healthz", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HTTP response must not send HSTS")
	}
}

func TestLoginLimiterPrunesExpiredEntries(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	for i := 0; i < 100; i++ {
		limiter.failure("192.0.2."+strconv.Itoa(i), now)
	}
	if len(limiter.entries) != 100 {
		t.Fatalf("entries = %d", len(limiter.entries))
	}
	limiter.blocked("198.51.100.1", now.Add(loginFailureWindow+time.Second))
	if len(limiter.entries) != 0 {
		t.Fatalf("expired entries were not pruned: %d", len(limiter.entries))
	}
}
