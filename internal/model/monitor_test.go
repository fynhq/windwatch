package model

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func validHTTPMonitor() Monitor {
	return Monitor{
		Name:             "Example",
		Type:             MonitorTypeHTTP,
		HTTP:             &HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299", FollowRedirects: true},
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    2,
		RecoverThreshold: 1,
		Enabled:          true,
		NotifyEnabled:    true,
	}
}

func TestMonitorValidateHTTP(t *testing.T) {
	monitor := validHTTPMonitor()
	if err := monitor.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	monitor.HTTP.URL = "ftp://example.com"
	if err := monitor.Validate(); !errors.Is(err, ErrHTTPURL) {
		t.Fatalf("URL error = %v, want ErrHTTPURL", err)
	}

	monitor = validHTTPMonitor()
	monitor.HTTP.Method = "POST"
	if err := monitor.Validate(); !errors.Is(err, ErrHTTPMethod) {
		t.Fatalf("method error = %v, want ErrHTTPMethod", err)
	}

	monitor = validHTTPMonitor()
	monitor.HTTP.ExpectedStatus = "200-100"
	if err := monitor.Validate(); !errors.Is(err, ErrHTTPStatus) {
		t.Fatalf("status error = %v, want ErrHTTPStatus", err)
	}

	monitor = validHTTPMonitor()
	monitor.HTTP.Method = "HEAD"
	monitor.HTTP.Keyword = "hello"
	if err := monitor.Validate(); !errors.Is(err, ErrHTTPKeyword) {
		t.Fatalf("keyword error = %v, want ErrHTTPKeyword", err)
	}

	monitor = validHTTPMonitor()
	monitor.Interval = 10 * time.Second
	if err := monitor.Validate(); !errors.Is(err, ErrInterval) {
		t.Fatalf("interval error = %v, want ErrInterval", err)
	}

	monitor = validHTTPMonitor()
	monitor.Timeout = 60 * time.Second
	if err := monitor.Validate(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout error = %v, want ErrTimeout", err)
	}

	monitor = validHTTPMonitor()
	monitor.FailThreshold = 0
	if err := monitor.Validate(); !errors.Is(err, ErrThreshold) {
		t.Fatalf("threshold error = %v, want ErrThreshold", err)
	}
}

func TestMonitorValidateDockerAndPush(t *testing.T) {
	docker := validHTTPMonitor()
	docker.Type = MonitorTypeDocker
	docker.HTTP = nil
	docker.Docker = &DockerConfig{Container: "windwatch", RequireHealthy: true}
	if err := docker.Validate(); err != nil {
		t.Fatalf("valid Docker monitor: %v", err)
	}
	docker.Docker.Container = ""
	if err := docker.Validate(); !errors.Is(err, ErrDockerContainer) {
		t.Fatalf("Docker container error = %v", err)
	}

	push := docker
	push.Type = MonitorTypePush
	push.Docker = nil
	if err := push.Validate(); err != nil {
		t.Fatalf("valid push monitor: %v", err)
	}
}

func TestHTTPSOptionalValidation(t *testing.T) {
	monitor := validHTTPMonitor()
	monitor.HTTP.CertCheck = true
	monitor.HTTP.CertWarnDays = 14
	if err := monitor.Validate(); err != nil {
		t.Fatalf("valid certificate check: %v", err)
	}

	monitor = validHTTPMonitor()
	monitor.HTTP.URL = "http://example.com"
	monitor.HTTP.CertCheck = true
	monitor.HTTP.CertWarnDays = 14
	if err := monitor.Validate(); !errors.Is(err, ErrHTTPSCert) {
		t.Fatalf("certificate over HTTP error = %v", err)
	}

	monitor = validHTTPMonitor()
	monitor.HTTP.DomainCheck = true
	monitor.HTTP.DomainWarnDays = 30
	if err := monitor.Validate(); err != nil {
		t.Fatalf("valid domain check: %v", err)
	}

	monitor.HTTP.URL = "https://127.0.0.1"
	if err := monitor.Validate(); !errors.Is(err, ErrDomainCheck) {
		t.Fatalf("IP domain check error = %v", err)
	}
}

func TestMonitorValidateTCP(t *testing.T) {
	monitor := Monitor{
		Name:             "Port",
		Type:             MonitorTypeTCP,
		TCP:              &TCPConfig{Host: "127.0.0.1", Port: 8080},
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    2,
		RecoverThreshold: 1,
	}
	if err := monitor.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := monitor.Target(); got != "127.0.0.1:8080" {
		t.Fatalf("Target() = %q", got)
	}

	monitor.TCP.Port = 0
	if err := monitor.Validate(); !errors.Is(err, ErrTCPPort) {
		t.Fatalf("port error = %v, want ErrTCPPort", err)
	}
	monitor.TCP.Port = 8080
	monitor.TCP.Host = "example.com\r\nX-Injected: yes"
	if err := monitor.Validate(); !errors.Is(err, ErrTCPHost) {
		t.Fatalf("control-character host error = %v, want ErrTCPHost", err)
	}
}

func TestConfigJSONRoundTrip(t *testing.T) {
	monitor := validHTTPMonitor()
	data, err := monitor.ConfigJSON()
	if err != nil {
		t.Fatalf("ConfigJSON: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	var decoded Monitor
	decoded.Type = MonitorTypeHTTP
	if err := decoded.DecodeConfig(data); err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if decoded.HTTP == nil || decoded.HTTP.URL != monitor.HTTP.URL {
		t.Fatalf("decoded config = %+v", decoded.HTTP)
	}
}

func TestParseExpectedStatus(t *testing.T) {
	tests := []struct {
		value   string
		min     int
		max     int
		wantErr bool
	}{
		{value: "", min: 200, max: 299},
		{value: "204", min: 204, max: 204},
		{value: "200-299", min: 200, max: 299},
		{value: "300 - 399", min: 300, max: 399},
		{value: "99", wantErr: true},
		{value: "200-100", wantErr: true},
		{value: "200,201", wantErr: true},
	}
	for _, test := range tests {
		minCode, maxCode, err := ParseExpectedStatus(test.value)
		if test.wantErr {
			if err == nil {
				t.Errorf("ParseExpectedStatus(%q) expected error", test.value)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseExpectedStatus(%q): %v", test.value, err)
			continue
		}
		if minCode != test.min || maxCode != test.max {
			t.Errorf("ParseExpectedStatus(%q) = %d-%d, want %d-%d", test.value, minCode, maxCode, test.min, test.max)
		}
	}
	if !StatusMatches("200-299", 204) {
		t.Fatal("204 should match 2xx")
	}
	if StatusMatches("200-299", 500) {
		t.Fatal("500 should not match 2xx")
	}
}
