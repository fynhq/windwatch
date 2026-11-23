package web

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/secret"
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

func TestLoginSuccessKeepsFailureWindow(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	setupAdminSession(t, testServer)
	hash, err := secret.HashPasswordContext(t.Context(), testPassword)
	if err != nil {
		t.Fatalf("hash viewer password: %v", err)
	}
	if _, err := st.CreateUser(t.Context(), "viewer", hash, model.RoleViewer, time.Now().UTC()); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	client := newTestClient(t)
	response, err := client.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	_ = readResponseBody(t, response)
	csrf := cookieValue(t, client, testServer.URL+"/login", csrfCookieName)
	for i := 0; i < maxLoginFailures-1; i++ {
		response, err = client.PostForm(testServer.URL+"/login", url.Values{
			"csrf_token": {csrf},
			"username":   {"admin"},
			"password":   {"wrong-password"},
		})
		if err != nil {
			t.Fatalf("POST failed login: %v", err)
		}
		_ = readResponseBody(t, response)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failed login status = %d, want 401", response.StatusCode)
		}
	}
	response, err = client.PostForm(testServer.URL+"/login", url.Values{
		"csrf_token": {csrf},
		"username":   {"viewer"},
		"password":   {testPassword},
	})
	if err != nil {
		t.Fatalf("POST viewer login: %v", err)
	}
	_ = readResponseBody(t, response)
	if cookieValue(t, client, testServer.URL+"/", sessionCookieName) == "" {
		t.Fatal("viewer login did not create a session")
	}

	other := newTestClient(t)
	response, err = other.Get(testServer.URL + "/login")
	if err != nil {
		t.Fatalf("GET login (other client): %v", err)
	}
	_ = readResponseBody(t, response)
	otherCSRF := cookieValue(t, other, testServer.URL+"/login", csrfCookieName)
	response, err = other.PostForm(testServer.URL+"/login", url.Values{
		"csrf_token": {otherCSRF},
		"username":   {"admin"},
		"password":   {"wrong-password"},
	})
	if err != nil {
		t.Fatalf("POST fifth guess: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("fifth guess status = %d, want 401", response.StatusCode)
	}
	response, err = other.PostForm(testServer.URL+"/login", url.Values{
		"csrf_token": {otherCSRF},
		"username":   {"admin"},
		"password":   {"wrong-password"},
	})
	if err != nil {
		t.Fatalf("POST sixth guess: %v", err)
	}
	_ = readResponseBody(t, response)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("guess after shared-IP success status = %d, want 429", response.StatusCode)
	}
}
