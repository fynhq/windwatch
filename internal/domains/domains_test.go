package domains

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/proxy"
)

func TestDomainForURL(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{url: "https://www.example.com/path", want: "example.com"},
		{url: "https://a.b.example.co.uk/", want: "example.co.uk"},
		{url: "https://example.com:8443", want: "example.com"},
		{url: "http://127.0.0.1/", wantErr: true},
		{url: "http://localhost/", wantErr: true},
		{url: "not a url", wantErr: true},
	}
	for _, test := range tests {
		got, err := DomainForURL(test.url)
		if test.wantErr {
			if err == nil {
				t.Errorf("DomainForURL(%q) expected error, got %q", test.url, got)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("DomainForURL(%q) = %q, %v; want %q", test.url, got, err, test.want)
		}
	}
}

func TestFetchRDAP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rdap+json")
		_, _ = w.Write([]byte(`{
			"status": ["clientTransferProhibited"],
			"events": [
				{"eventAction": "registration", "eventDate": "2020-01-01T00:00:00Z"},
				{"eventAction": "expiration", "eventDate": "2027-01-01T00:00:00Z"}
			],
			"entities": [
				{"roles": ["registrar"], "vcardArray": ["vcard", [["fn", {}, "text", "Example Registrar"]]]}
			]
		}`))
	}))
	defer server.Close()

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	info, err := fetchRDAP(context.Background(), server.URL+"/domain/", "example.com", func() (*proxy.Config, error) {
		return nil, nil
	}, now)
	if err != nil {
		t.Fatalf("fetchRDAP: %v", err)
	}
	if info.Registrar != "Example Registrar" || len(info.Statuses) != 1 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if !info.ExpiresAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expiration = %s", info.ExpiresAt)
	}
}

func TestParseRDAPRequiresExpiration(t *testing.T) {
	if _, err := parseRDAP([]byte(`{"status":[]}`)); err == nil {
		t.Fatal("expected missing expiration error")
	}
}
