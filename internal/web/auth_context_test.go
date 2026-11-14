package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fynhq/windwatch/internal/model"
)

func TestErrorPageUsesResolvedAuthOnly(t *testing.T) {
	srv, _, _ := newWebTestServer(t)
	auth := model.AuthInfo{
		Admin:   model.Admin{Username: "context-user", Role: model.RoleAdmin},
		Session: model.Session{CSRFToken: "csrf"},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	srv.renderError(recorder, withAuthContext(request, auth), http.StatusNotFound, "error.notFound")
	if body := recorder.Body.String(); !strings.Contains(body, "context-user") {
		t.Fatalf("authenticated error page lost the resolved user: %s", body)
	}

	recorder = httptest.NewRecorder()
	srv.renderError(recorder, request, http.StatusNotFound, "error.notFound")
	if body := recorder.Body.String(); strings.Contains(body, "context-user") {
		t.Fatalf("anonymous error page exposed the resolved user: %s", body)
	}
}

func TestAuthFromContextRequiresExplicitResolution(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := authFromContext(request); ok {
		t.Fatal("unresolved request unexpectedly carried auth")
	}
	auth := model.AuthInfo{Admin: model.Admin{Username: "viewer"}}
	resolved, ok := authFromContext(withAuthContext(request, auth))
	if !ok || resolved.Admin.Username != "viewer" {
		t.Fatalf("resolved auth = %+v ok=%v", resolved, ok)
	}
}
