package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProxyConnectivityTest(t *testing.T) {
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("proxy method = %s, want CONNECT", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer proxyServer.Close()
	address := strings.TrimPrefix(proxyServer.URL, "http://")

	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.PostForm(testServer.URL+"/settings/proxies/test", url.Values{
		"csrf_token": {csrf},
		"name":       {"Candidate proxy"},
		"type":       {"http"},
		"address":    {address},
	})
	if err != nil {
		t.Fatalf("POST proxy test: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "Proxy connectivity test succeeded.") {
		t.Fatalf("proxy test status=%d body=%s", response.StatusCode, body)
	}
	proxies, err := st.ListProxies(t.Context())
	if err != nil || len(proxies) != 0 {
		t.Fatalf("proxy test saved a proxy: %+v err=%v", proxies, err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	unreachable := listener.Addr().String()
	_ = listener.Close()
	response, err = client.PostForm(testServer.URL+"/settings/proxies/test", url.Values{
		"csrf_token": {csrf},
		"name":       {"Broken proxy"},
		"type":       {"http"},
		"address":    {unreachable},
	})
	if err != nil {
		t.Fatalf("POST broken proxy test: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "Proxy connectivity test failed. Check the address, type, and credentials.") {
		t.Fatalf("broken proxy test status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, unreachable) {
		t.Fatalf("proxy test detail missing: %s", body)
	}
}
