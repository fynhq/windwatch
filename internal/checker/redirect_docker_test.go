package checker

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestDockerVersionRefreshAfterDaemonRejectsCachedVersion(t *testing.T) {
	var versionHits, oldHits, newHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			versionHits.Add(1)
			_, _ = w.Write([]byte(`{"ApiVersion":"1.46"}`))
		case "/v1.45/containers/demo/json":
			oldHits.Add(1)
			http.Error(w, "client version too old", http.StatusBadRequest)
		case "/v1.46/containers/demo/json":
			newHits.Add(1)
			_, _ = w.Write([]byte(`{"Id":"abc","Name":"/demo","State":{"Running":true,"Status":"running"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	checker := &dockerChecker{
		client:     server.Client(),
		baseURL:    server.URL,
		apiVersion: "1.45",
	}
	monitor := model.Monitor{
		Name: "Docker", Type: model.MonitorTypeDocker,
		Docker:   &model.DockerConfig{Container: "demo"},
		Interval: 60 * time.Second, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1,
	}
	result, err := checker.Check(t.Context(), Request{Monitor: monitor})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %+v, want success after API version refresh", result)
	}
	if oldHits.Load() != 1 || newHits.Load() != 1 || versionHits.Load() != 1 {
		t.Fatalf("hits old=%d new=%d version=%d, want 1/1/1", oldHits.Load(), newHits.Load(), versionHits.Load())
	}
	if checker.apiVersion != "1.46" {
		t.Fatalf("cached version = %q, want 1.46", checker.apiVersion)
	}
}

func TestDockerVersionFailureIsCachedBriefly(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			hits.Add(1)
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	checker := &dockerChecker{client: server.Client(), baseURL: server.URL}
	monitor := model.Monitor{
		Name: "Docker", Type: model.MonitorTypeDocker,
		Docker:   &model.DockerConfig{Container: "demo"},
		Interval: 60 * time.Second, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1,
	}
	for i := 0; i < 2; i++ {
		if _, err := checker.Check(t.Context(), Request{Monitor: monitor}); err != nil {
			t.Fatalf("Check %d: %v", i, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("/version hits = %d, want 1 cached failure", hits.Load())
	}
}

func TestHTTPCertificateCapturedBeforeRedirectToHTTP(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer tlsServer.Close()

	checker := newHTTPChecker()
	monitor := model.Monitor{
		Name: "Redirect", Type: model.MonitorTypeHTTP,
		HTTP: &model.HTTPConfig{
			URL: tlsServer.URL, Method: http.MethodGet, ExpectedStatus: "200-299",
			FollowRedirects: true, TLSSkipVerify: true, CertCheck: true, CertWarnDays: 30,
		},
		Interval: 60 * time.Second, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1,
	}
	result, err := checker.Check(t.Context(), Request{Monitor: monitor})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.Success || result.Certificate == nil || result.Certificate.Fingerprint == "" {
		t.Fatalf("result = %+v, want success with certificate from first HTTPS hop", result)
	}
}
