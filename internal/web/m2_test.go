package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func setupAdminSession(t *testing.T, testServer *httptest.Server) (*http.Client, string) {
	t.Helper()
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
	return client, extractCSRF(t, body)
}

func TestProxySettingsAndMonitorSelection(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.Get(testServer.URL + "/settings/proxies")
	if err != nil {
		t.Fatalf("GET /settings/proxies: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Outbound proxies") {
		t.Fatalf("settings status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/proxies", url.Values{
		"csrf_token": {csrf},
		"name":       {"Local proxy"},
		"type":       {"http"},
		"address":    {"127.0.0.1:7890"},
		"username":   {"user"},
		"password":   {"proxy-password"},
	})
	if err != nil {
		t.Fatalf("POST proxy: %v", err)
	}
	_ = readResponseBody(t, response)
	proxies, err := st.ListProxies(t.Context())
	if err != nil || len(proxies) != 1 {
		t.Fatalf("proxies=%+v err=%v", proxies, err)
	}
	if proxies[0].PasswordEnc == "" || proxies[0].PasswordEnc == "proxy-password" {
		t.Fatal("proxy password was not encrypted")
	}
	password, err := srv.cipher.Decrypt(proxies[0].PasswordEnc)
	if err != nil || password != "proxy-password" {
		t.Fatalf("decrypted password=%q err=%v", password, err)
	}
	proxyID := proxies[0].ID

	response, err = client.Get(testServer.URL + "/monitors/new?type=http")
	if err != nil {
		t.Fatalf("GET HTTP form: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Local proxy") {
		t.Fatalf("proxy option missing from form: %s", body)
	}

	response, err = client.PostForm(testServer.URL+"/monitors", url.Values{
		"csrf_token":        {csrf},
		"type":              {"http"},
		"name":              {"Through proxy"},
		"url":               {"https://example.com"},
		"method":            {"GET"},
		"expected_status":   {"200-299"},
		"follow_redirects":  {"1"},
		"interval":          {"60"},
		"timeout":           {"10"},
		"fail_threshold":    {"2"},
		"recover_threshold": {"1"},
		"proxy_id":          {strconv.FormatInt(proxyID, 10)},
		"cert_warn_days":    {"14"},
		"domain_warn_days":  {"30"},
	})
	if err != nil {
		t.Fatalf("POST proxied monitor: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Through proxy") {
		t.Fatalf("proxied monitor status=%d body=%s", response.StatusCode, body)
	}
	monitors, err := st.ListMonitors(t.Context())
	if err != nil || len(monitors) != 1 || monitors[0].ProxyID == nil || *monitors[0].ProxyID != proxyID {
		t.Fatalf("monitor proxy was not stored: %+v err=%v", monitors, err)
	}
}

func TestPushMonitorAndHeartbeatEndpoint(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.PostForm(testServer.URL+"/monitors", url.Values{
		"csrf_token":        {csrf},
		"type":              {"push"},
		"name":              {"Nightly backup"},
		"interval":          {"60"},
		"timeout":           {"10"},
		"fail_threshold":    {"2"},
		"recover_threshold": {"1"},
	})
	if err != nil {
		t.Fatalf("POST push monitor: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Heartbeat URL") {
		t.Fatalf("push detail status=%d body=%s", response.StatusCode, body)
	}

	monitors, err := st.ListMonitors(t.Context())
	if err != nil || len(monitors) != 1 {
		t.Fatalf("monitors=%+v err=%v", monitors, err)
	}
	token, err := st.PushTokenByMonitorID(t.Context(), monitors[0].ID)
	if err != nil {
		t.Fatalf("PushTokenByMonitorID: %v", err)
	}
	plaintext, err := srv.cipher.Decrypt(token.TokenEnc)
	if err != nil {
		t.Fatalf("decrypt push token: %v", err)
	}

	response, err = client.Get(testServer.URL + "/api/push/" + plaintext)
	if err != nil {
		t.Fatalf("GET push endpoint: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("push endpoint status=%d", response.StatusCode)
	}
	token, err = st.PushTokenByMonitorID(t.Context(), monitors[0].ID)
	if err != nil || token.LastHeartbeat.IsZero() {
		t.Fatalf("heartbeat was not recorded: %+v err=%v", token, err)
	}

	response, err = client.Get(testServer.URL + "/api/push/not-a-real-token")
	if err != nil {
		t.Fatalf("GET bad push endpoint: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("bad push endpoint status=%d, want 404", response.StatusCode)
	}
}

func TestDockerMonitorCreate(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.PostForm(testServer.URL+"/monitors", url.Values{
		"csrf_token":        {csrf},
		"type":              {"docker"},
		"name":              {"Container"},
		"docker_container":  {"windwatch"},
		"docker_healthy":    {"1"},
		"interval":          {"60"},
		"timeout":           {"10"},
		"fail_threshold":    {"2"},
		"recover_threshold": {"1"},
	})
	if err != nil {
		t.Fatalf("POST Docker monitor: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Container") {
		t.Fatalf("Docker detail status=%d body=%s", response.StatusCode, body)
	}
	monitors, err := st.ListMonitors(t.Context())
	if err != nil || len(monitors) != 1 || monitors[0].Docker == nil || monitors[0].Docker.Container != "windwatch" {
		t.Fatalf("Docker monitor was not stored: %+v err=%v", monitors, err)
	}
}

func TestTimezoneSettings(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.Get(testServer.URL + "/settings/general")
	if err != nil {
		t.Fatalf("GET /settings/general: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `name="timezone"`) ||
		!strings.Contains(body, `value="UTC"`) ||
		!strings.Contains(body, `list="timezone-options"`) ||
		!strings.Contains(body, `<datalist id="timezone-options">`) ||
		!strings.Contains(body, `value="Asia/Shanghai"`) ||
		strings.Contains(body, `value="Asia/Kathmandu"`) {
		t.Fatalf("timezone options missing: status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/timezone", url.Values{
		"csrf_token": {csrf},
		"timezone":   {"Asia/Shanghai"},
	})
	if err != nil {
		t.Fatalf("POST timezone: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, `name="timezone" value="Asia/Shanghai"`) {
		t.Fatalf("timezone not saved: status=%d body=%s", response.StatusCode, body)
	}
	values, err := st.Settings(t.Context())
	if err != nil || values["timezone"] != "Asia/Shanghai" {
		t.Fatalf("stored timezone = %q err=%v", values["timezone"], err)
	}

	response, err = client.PostForm(testServer.URL+"/settings/timezone", url.Values{
		"csrf_token": {csrf},
		"timezone":   {"Asia/Kathmandu"},
	})
	if err != nil {
		t.Fatalf("POST invalid timezone: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Choose a valid timezone.") {
		t.Fatalf("invalid timezone status=%d body=%s", response.StatusCode, body)
	}
	values, err = st.Settings(t.Context())
	if err != nil || values["timezone"] != "Asia/Shanghai" {
		t.Fatalf("timezone changed after invalid save: %q err=%v", values["timezone"], err)
	}
}
