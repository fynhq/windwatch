package web

import (
	"context"
	"testing"
	"time"
)

func TestPublicSnapshotBuildPanicDoesNotStick(t *testing.T) {
	srv, _, _ := newWebTestServer(t)
	now := time.Now().UTC()
	savedStore := srv.store
	srv.store = nil
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected the injected build panic")
			}
		}()
		_, _ = srv.loadPublicSnapshot(t.Context(), []int64{1}, 1, now, time.UTC)
	}()
	srv.store = savedStore

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := srv.loadPublicSnapshot(ctx, []int64{1}, 1, now.Add(time.Second), time.UTC); err != nil {
		t.Fatalf("public snapshot stayed stuck after a build panic: %v", err)
	}
}

func TestPublicSnapshotRefreshesWhitelistOnVersionChange(t *testing.T) {
	srv, _, _ := newWebTestServer(t)
	firstID := createHTTPMonitor(t, srv, "First", "https://first.example.com")
	secondID := createHTTPMonitor(t, srv, "Second", "https://second.example.com")
	now := time.Now().UTC()

	first, err := srv.loadPublicSnapshot(t.Context(), []int64{firstID}, 1, now, time.UTC)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if len(first.monitors) != 1 || first.monitors[0].ID != firstID {
		t.Fatalf("first snapshot monitors = %+v", first.monitors)
	}
	second, err := srv.loadPublicSnapshot(t.Context(), []int64{secondID}, 2, now.Add(time.Second), time.UTC)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if len(second.monitors) != 1 || second.monitors[0].ID != secondID {
		t.Fatalf("second snapshot monitors = %+v", second.monitors)
	}
}
