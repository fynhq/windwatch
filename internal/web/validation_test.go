package web

import (
	"net/http"
	"net/url"
	"testing"
)

func TestMonitorFormRejectsOverflowingDurations(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	cases := []struct {
		name   string
		values url.Values
	}{
		{
			name: "interval overflow",
			values: url.Values{
				"csrf_token": {csrf}, "type": {"http"}, "name": {"Overflow"},
				"url": {"https://example.com"}, "method": {"GET"}, "expected_status": {"200-299"},
				"interval": {"18446744094"}, "timeout": {"10"},
				"fail_threshold": {"2"}, "recover_threshold": {"1"},
			},
		},
		{
			name: "reminder interval overflow",
			values: url.Values{
				"csrf_token": {csrf}, "type": {"http"}, "name": {"Overflow"},
				"url": {"https://example.com"}, "method": {"GET"}, "expected_status": {"200-299"},
				"interval": {"60"}, "timeout": {"10"},
				"fail_threshold": {"2"}, "recover_threshold": {"1"},
				"failure_reminder_interval_minutes": {"9223372036854775807"},
			},
		},
	}
	for _, testCase := range cases {
		response, err := client.PostForm(testServer.URL+"/monitors", testCase.values)
		if err != nil {
			t.Fatalf("%s: POST: %v", testCase.name, err)
		}
		_ = readResponseBody(t, response)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", testCase.name, response.StatusCode)
		}
	}
	monitors, err := st.ListMonitors(t.Context())
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(monitors) != 0 {
		t.Fatalf("overflowing forms created monitors: %+v", monitors)
	}
}
