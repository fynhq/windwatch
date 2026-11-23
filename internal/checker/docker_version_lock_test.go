package checker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDockerVersionFetchDoesNotSerializeConcurrentChecks(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			<-release
			_ = json.NewEncoder(w).Encode(map[string]string{"ApiVersion": "1.45"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	defer close(release)

	d := &dockerChecker{client: server.Client(), baseURL: server.URL}
	ctx1, cancel1 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel1()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()

	errs := make([]error, 2)
	var shortElapsed time.Duration
	var wg sync.WaitGroup
	wg.Add(2)
	start := time.Now()
	go func() {
		defer wg.Done()
		_, errs[0] = d.getAPIVersion(ctx1)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = d.getAPIVersion(ctx2)
		shortElapsed = time.Since(start)
	}()
	wg.Wait()

	if errs[0] == nil || errs[1] == nil {
		t.Fatalf("expected both version fetches to fail, got %v / %v", errs[0], errs[1])
	}
	if shortElapsed > 300*time.Millisecond {
		t.Fatalf("short-deadline check waited %s for the long version fetch; lock must not span the network call", shortElapsed)
	}
}
