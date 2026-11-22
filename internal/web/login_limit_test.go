package web

import (
	"net/http"
	"net/url"
	"testing"
)

func TestLoginInFlightLimit(t *testing.T) {
	srv, _, testServer := newWebTestServer(t)
	setupAdminSession(t, testServer)
	client := newTestClient(t)
	response, err := client.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET login status = %d", response.StatusCode)
	}
	_ = readResponseBody(t, response)
	csrf := cookieValue(t, client, testServer.URL+"/login", csrfCookieName)
	for i := 0; i < maxLoginInFlight; i++ {
		srv.loginSlots <- struct{}{}
	}
	response, err = client.PostForm(testServer.URL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {"admin"},
		"password":   {testPassword},
	})
	if err != nil {
		t.Fatalf("POST login: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("saturated login status = %d, want 429", response.StatusCode)
	}
}
