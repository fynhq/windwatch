package web

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestProxyFormRejectsForeignCiphertext(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)
	foreign, err := srv.cipher.Encrypt("victim-token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	wrongPurpose, err := srv.cipher.EncryptWithPurpose("other-purpose", "victim-token")
	if err != nil {
		t.Fatalf("EncryptWithPurpose: %v", err)
	}
	for _, ciphertext := range []string{foreign, wrongPurpose} {
		response, err := client.PostForm(testServer.URL+"/settings/proxies/test", url.Values{
			"csrf_token":          {csrf},
			"name":                {"Replay"},
			"type":                {"http"},
			"address":             {"127.0.0.1:9"},
			"password_ciphertext": {ciphertext},
		})
		if err != nil {
			t.Fatalf("POST proxy test: %v", err)
		}
		body := readResponseBody(t, response)
		if !strings.Contains(body, "The request could not be processed.") {
			t.Fatalf("foreign ciphertext accepted on test: status=%d", response.StatusCode)
		}
		response, err = client.PostForm(testServer.URL+"/settings/proxies", url.Values{
			"csrf_token":          {csrf},
			"name":                {"Replay"},
			"type":                {"http"},
			"address":             {"127.0.0.1:9"},
			"password_ciphertext": {ciphertext},
		})
		if err != nil {
			t.Fatalf("POST proxy create: %v", err)
		}
		body = readResponseBody(t, response)
		if !strings.Contains(body, "The request could not be processed.") {
			t.Fatalf("foreign ciphertext accepted on save: status=%d", response.StatusCode)
		}
	}
	proxies, err := st.ListProxies(t.Context())
	if err != nil || len(proxies) != 0 {
		t.Fatalf("foreign ciphertext persisted: proxies=%+v err=%v", proxies, err)
	}
}

func TestProxyEditRoundTripKeepsStoredPassword(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)
	response, err := client.PostForm(testServer.URL+"/settings/proxies", url.Values{
		"csrf_token": {csrf},
		"name":       {"Round trip"},
		"type":       {"http"},
		"address":    {"127.0.0.1:9"},
		"password":   {"store-me"},
	})
	if err != nil {
		t.Fatalf("POST proxy create: %v", err)
	}
	_ = readResponseBody(t, response)
	proxies, err := st.ListProxies(t.Context())
	if err != nil || len(proxies) != 1 {
		t.Fatalf("proxies=%+v err=%v", proxies, err)
	}
	stored := proxies[0]
	response, err = client.PostForm(fmt.Sprintf("%s/settings/proxies/%d", testServer.URL, stored.ID), url.Values{
		"csrf_token":          {csrf},
		"name":                {"Round trip"},
		"type":                {"http"},
		"address":             {"127.0.0.1:9"},
		"password_ciphertext": {stored.PasswordEnc},
	})
	if err != nil {
		t.Fatalf("POST proxy update: %v", err)
	}
	body := readResponseBody(t, response)
	if strings.Contains(body, "The request could not be processed.") {
		t.Fatalf("stored ciphertext round trip rejected: %s", body)
	}
	proxies, err = st.ListProxies(t.Context())
	if err != nil || len(proxies) != 1 {
		t.Fatalf("proxies=%+v err=%v", proxies, err)
	}
	password, err := srv.cipher.Decrypt(proxies[0].PasswordEnc)
	if err != nil || password != "store-me" {
		t.Fatalf("stored password = %q err=%v", password, err)
	}
}
