package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestProxyTestPreservesCandidatePassword(t *testing.T) {
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer proxyServer.Close()
	address := strings.TrimPrefix(proxyServer.URL, "http://")

	srv, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)
	response, err := client.PostForm(testServer.URL+"/settings/proxies/test", url.Values{
		"csrf_token": {csrf},
		"name":       {"Candidate proxy"},
		"type":       {"http"},
		"address":    {address},
		"username":   {"user"},
		"password":   {"proxy-secret"},
	})
	if err != nil {
		t.Fatalf("POST proxy test: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxy test status=%d body=%s", response.StatusCode, body)
	}
	match := regexp.MustCompile(`name="password_ciphertext" value="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("candidate password was not retained in the form: %s", body)
	}
	if strings.Contains(body, "proxy-secret") {
		t.Fatal("plaintext proxy password appeared in the response")
	}
	ciphertext := html.UnescapeString(match[1])
	if password, err := srv.cipher.Decrypt(ciphertext); err != nil || password != "proxy-secret" {
		t.Fatalf("candidate ciphertext = %q password = %q err=%v", ciphertext, password, err)
	}

	response, err = client.PostForm(testServer.URL+"/settings/proxies", url.Values{
		"csrf_token":          {csrf},
		"name":                {"Candidate proxy"},
		"type":                {"http"},
		"address":             {address},
		"username":            {"user"},
		"password_ciphertext": {ciphertext},
	})
	if err != nil {
		t.Fatalf("POST proxy create: %v", err)
	}
	createBody := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxy create status=%d body=%s", response.StatusCode, createBody)
	}
	proxies, err := st.ListProxies(t.Context())
	if err != nil || len(proxies) != 1 {
		t.Fatalf("proxies=%+v err=%v", proxies, err)
	}
	password, err := srv.cipher.Decrypt(proxies[0].PasswordEnc)
	if err != nil || password != "proxy-secret" {
		t.Fatalf("stored password = %q err=%v", password, err)
	}
}

func TestInvalidNotificationChannelTypeIsRejected(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)
	response, err := client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token": {csrf},
		"name":       {"Invalid"},
		"type":       {"slack"},
		"language":   {"en"},
	})
	if err != nil {
		t.Fatalf("POST invalid channel: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid channel status = %d, want 400", response.StatusCode)
	}
	channels, err := st.ListNotificationChannels(t.Context())
	if err != nil || len(channels) != 0 {
		t.Fatalf("channels=%+v err=%v", channels, err)
	}
}

func TestLanguageLinkPreservesQuery(t *testing.T) {
	_, _, testServer := newWebTestServer(t)
	client, _ := setupAdminSession(t, testServer)
	response, err := client.Get(testServer.URL + "/monitors/new?type=http")
	if err != nil {
		t.Fatalf("GET monitor form: %v", err)
	}
	body := readResponseBody(t, response)
	expected := "next=" + url.QueryEscape("/monitors/new?type=http")
	if !strings.Contains(body, expected) {
		t.Fatalf("language link lost query: want %q in %s", expected, body)
	}
}

func TestLongRangeUptimeIncludesBoundaryBucketAfterCleanup(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	adminClient, _ := setupAdminSession(t, testServer)
	id := createHTTPMonitor(t, srv, "Long range", "https://example.com/health")

	now := time.Date(2026, 11, 7, 12, 15, 0, 0, time.UTC)
	checkedAt := longRangeStart(now, 30).Add(5 * time.Minute)
	state := model.MonitorState{
		Status: model.StatusUp, StatusSince: checkedAt, LastCheck: checkedAt, UpdatedAt: checkedAt,
	}
	if err := st.RecordCheck(t.Context(), model.Check{
		MonitorID: id, CheckedAt: checkedAt, Status: model.CheckUp, Latency: time.Millisecond, MetaJSON: "{}",
	}, state, nil, false); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if err := st.AggregateHalfHourlyRollups(t.Context(), checkedAt, now, now); err != nil {
		t.Fatalf("AggregateHalfHourlyRollups: %v", err)
	}
	if err := st.DeleteChecksBefore(t.Context(), now.Add(-7*24*time.Hour)); err != nil {
		t.Fatalf("DeleteChecksBefore: %v", err)
	}
	srv.now = func() time.Time { return now }

	response, err := adminClient.Get(testServer.URL + "/monitors/" + strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatalf("GET monitor detail: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, "100.00%") {
		t.Fatalf("long-range boundary bucket was omitted after raw cleanup: %s", body)
	}
}
