package notify

import (
	"errors"
	"testing"
)

func TestResolveProxyKeepsTransientStoreErrorsRetryable(t *testing.T) {
	service, st := testNotifyService(t)
	if _, err := service.resolveProxy(t.Context(), 9999); err == nil {
		t.Fatal("missing proxy should return an error")
	} else {
		var permanent permanentError
		if !errors.As(err, &permanent) {
			t.Fatalf("missing proxy error should be permanent: %v", err)
		}
	}

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := service.resolveProxy(t.Context(), 9999); err == nil {
		t.Fatal("closed store should return an error")
	} else {
		var permanent permanentError
		if errors.As(err, &permanent) {
			t.Fatalf("store failure must stay retryable: %v", err)
		}
	}
}
