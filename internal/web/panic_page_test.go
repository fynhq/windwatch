package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanicRecoveryPageIsLocalized(t *testing.T) {
	srv, _, _ := newWebTestServer(t)
	handler := srv.withPanicRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "An internal error occurred.") {
		t.Fatalf("panic body = %q, want localized English message", body)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: languageCookieName, Value: "zh-CN"})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if body := recorder.Body.String(); !strings.Contains(body, "发生内部错误。") {
		t.Fatalf("panic body = %q, want localized Chinese message", body)
	}
}
