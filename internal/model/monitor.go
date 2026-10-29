package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type MonitorType string

const (
	MonitorTypeHTTP   MonitorType = "http"
	MonitorTypeTCP    MonitorType = "tcp"
	MonitorTypeDocker MonitorType = "docker"
	MonitorTypePush   MonitorType = "push"
)

type MonitorStatus string

const (
	StatusPending MonitorStatus = "pending"
	StatusUp      MonitorStatus = "up"
	StatusWarning MonitorStatus = "warning"
	StatusDown    MonitorStatus = "down"
)

const (
	CheckUp      = "up"
	CheckWarning = "warning"
	CheckDown    = "down"
)

type HTTPConfig struct {
	URL             string `json:"url"`
	Method          string `json:"method"`
	ExpectedStatus  string `json:"expected_status"`
	Keyword         string `json:"keyword,omitempty"`
	FollowRedirects bool   `json:"follow_redirects"`
	TLSSkipVerify   bool   `json:"tls_skip_verify,omitempty"`
	CertCheck       bool   `json:"cert_check,omitempty"`
	CertWarnDays    int    `json:"cert_warn_days,omitempty"`
	DomainCheck     bool   `json:"domain_check,omitempty"`
	DomainWarnDays  int    `json:"domain_warn_days,omitempty"`
}

type TCPConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type DockerConfig struct {
	Container      string `json:"container"`
	RequireHealthy bool   `json:"require_healthy"`
}

type Monitor struct {
	ID                            int64
	Name                          string
	Type                          MonitorType
	HTTP                          *HTTPConfig
	TCP                           *TCPConfig
	Docker                        *DockerConfig
	ProxyID                       *int64
	NotificationChannelIDs        []int64
	EnabledNotificationChannelIDs []int64
	Interval                      time.Duration
	Timeout                       time.Duration
	FailThreshold                 int
	RecoverThreshold              int
	Reminders                     NotificationPolicy
	NotifyEnabled                 bool
	Enabled                       bool
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	State                         MonitorState
}

type MonitorState struct {
	Status               MonitorStatus
	StatusSince          time.Time
	LastCheck            time.Time
	LastError            string
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	UpdatedAt            time.Time
}

type CertificateInfo struct {
	Fingerprint string
	Subject     string
	Issuer      string
	SANs        []string
	NotBefore   time.Time
	NotAfter    time.Time
}

type CheckResult struct {
	Success      bool
	Latency      time.Duration
	HTTPStatus   int
	ErrorKind    string
	ErrorMessage string
	MetaJSON     string
	Certificate  *CertificateInfo
}

type Check struct {
	MonitorID       int64
	CheckedAt       time.Time
	Status          string
	Latency         time.Duration
	HTTPStatus      int
	ErrorKind       string
	ErrorMessage    string
	MetaJSON        string
	CertFingerprint string
}

type EventType string

const (
	EventDown       EventType = "down"
	EventUp         EventType = "up"
	EventWarnCert   EventType = "warn_cert"
	EventWarnDomain EventType = "warn_domain"
)

type Event struct {
	ID          int64
	MonitorID   int64
	MonitorName string
	Type        EventType
	Message     string
	StartedAt   time.Time
	EndedAt     *time.Time
	MetaJSON    string
}

type UptimeStats struct {
	Total      int
	Up         int
	Warning    int
	Down       int
	AvgLatency time.Duration
}

type HourBucket struct {
	MonitorID  int64
	Start      time.Time
	Total      int
	Up         int
	Warning    int
	Down       int
	AvgLatency time.Duration
}

const (
	ErrorKindTimeout            = "timeout"
	ErrorKindDNS                = "dns_error"
	ErrorKindConnectionRefused  = "connect_refused"
	ErrorKindTLS                = "tls_error"
	ErrorKindInvalidStatus      = "invalid_status"
	ErrorKindKeywordMissing     = "keyword_missing"
	ErrorKindNetwork            = "network_error"
	ErrorKindInvalidConfig      = "invalid_config"
	ErrorKindInternal           = "internal_error"
	ErrorKindContainerMissing   = "container_missing"
	ErrorKindContainerStopped   = "container_stopped"
	ErrorKindContainerUnhealthy = "container_unhealthy"
	ErrorKindDockerUnavailable  = "docker_unavailable"
	ErrorKindHeartbeatTimeout   = "heartbeat_timeout"
	ErrorKindProxy              = "proxy_error"
)

var (
	ErrMonitorName     = errors.New("invalid monitor name")
	ErrMonitorType     = errors.New("invalid monitor type")
	ErrInterval        = errors.New("invalid monitor interval")
	ErrTimeout         = errors.New("invalid monitor timeout")
	ErrThreshold       = errors.New("invalid monitor threshold")
	ErrHTTPURL         = errors.New("invalid HTTP URL")
	ErrHTTPMethod      = errors.New("invalid HTTP method")
	ErrHTTPStatus      = errors.New("invalid expected HTTP status")
	ErrHTTPKeyword     = errors.New("invalid HTTP keyword")
	ErrTCPHost         = errors.New("invalid TCP host")
	ErrTCPPort         = errors.New("invalid TCP port")
	ErrDockerContainer = errors.New("invalid Docker container")
	ErrHTTPSCert       = errors.New("certificate checks require HTTPS")
	ErrDomainCheck     = errors.New("domain checks require an HTTPS public domain")
)

func (m Monitor) Validate() error {
	name := strings.TrimSpace(m.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return ErrMonitorName
	}
	if m.Interval < 20*time.Second || m.Interval > 24*time.Hour {
		return ErrInterval
	}
	if m.Timeout < time.Second || m.Timeout > 5*time.Minute || m.Timeout >= m.Interval {
		return ErrTimeout
	}
	if m.FailThreshold < 1 || m.FailThreshold > 20 || m.RecoverThreshold < 1 || m.RecoverThreshold > 20 {
		return ErrThreshold
	}
	if err := m.Reminders.Validate(); err != nil {
		return err
	}

	switch m.Type {
	case MonitorTypeHTTP:
		if m.HTTP == nil {
			return ErrHTTPURL
		}
		if err := validateHTTPConfig(*m.HTTP); err != nil {
			return err
		}
	case MonitorTypeTCP:
		if m.TCP == nil {
			return ErrTCPHost
		}
		if err := validateTCPConfig(*m.TCP); err != nil {
			return err
		}
	case MonitorTypeDocker:
		if m.Docker == nil || strings.TrimSpace(m.Docker.Container) == "" {
			return ErrDockerContainer
		}
	case MonitorTypePush:
		// Push monitors use Interval as the expected heartbeat interval and
		// Timeout as the grace period.
	default:
		return ErrMonitorType
	}
	return nil
}

func (m Monitor) ConfigJSON() ([]byte, error) {
	switch m.Type {
	case MonitorTypeHTTP:
		if m.HTTP == nil {
			return nil, ErrHTTPURL
		}
		data, err := json.Marshal(m.HTTP)
		if err != nil {
			return nil, fmt.Errorf("marshal HTTP config: %w", err)
		}
		return data, nil
	case MonitorTypeTCP:
		if m.TCP == nil {
			return nil, ErrTCPHost
		}
		data, err := json.Marshal(m.TCP)
		if err != nil {
			return nil, fmt.Errorf("marshal TCP config: %w", err)
		}
		return data, nil
	case MonitorTypeDocker:
		if m.Docker == nil {
			return nil, ErrDockerContainer
		}
		data, err := json.Marshal(m.Docker)
		if err != nil {
			return nil, fmt.Errorf("marshal Docker config: %w", err)
		}
		return data, nil
	case MonitorTypePush:
		return []byte("{}"), nil
	default:
		return nil, ErrMonitorType
	}
}

func (m *Monitor) DecodeConfig(data []byte) error {
	switch m.Type {
	case MonitorTypeHTTP:
		var cfg HTTPConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("decode HTTP config: %w", err)
		}
		m.HTTP = &cfg
		m.TCP = nil
	case MonitorTypeTCP:
		var cfg TCPConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("decode TCP config: %w", err)
		}
		m.TCP = &cfg
		m.HTTP = nil
		m.Docker = nil
	case MonitorTypeDocker:
		var cfg DockerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("decode Docker config: %w", err)
		}
		m.Docker = &cfg
		m.HTTP = nil
		m.TCP = nil
	case MonitorTypePush:
		m.HTTP = nil
		m.TCP = nil
		m.Docker = nil
	default:
		return ErrMonitorType
	}
	return nil
}

func (m Monitor) Target() string {
	switch m.Type {
	case MonitorTypeHTTP:
		if m.HTTP != nil {
			return m.HTTP.URL
		}
	case MonitorTypeTCP:
		if m.TCP != nil {
			return net.JoinHostPort(strings.TrimSpace(m.TCP.Host), strconv.Itoa(m.TCP.Port))
		}
	case MonitorTypeDocker:
		if m.Docker != nil {
			return m.Docker.Container
		}
	case MonitorTypePush:
		return "Heartbeat"
	}
	return ""
}

func ParseExpectedStatus(value string) (int, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 200, 299, nil
	}
	parts := strings.Split(value, "-")
	if len(parts) > 2 {
		return 0, 0, ErrHTTPStatus
	}
	minCode, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || minCode < 100 || minCode > 599 {
		return 0, 0, ErrHTTPStatus
	}
	maxCode := minCode
	if len(parts) == 2 {
		maxCode, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || maxCode < 100 || maxCode > 599 || maxCode < minCode {
			return 0, 0, ErrHTTPStatus
		}
	}
	return minCode, maxCode, nil
}

func StatusMatches(expectation string, code int) bool {
	minCode, maxCode, err := ParseExpectedStatus(expectation)
	if err != nil {
		return false
	}
	return code >= minCode && code <= maxCode
}

func validateHTTPConfig(cfg HTTPConfig) error {
	trimmedURL := strings.TrimSpace(cfg.URL)
	if trimmedURL == "" || len(trimmedURL) > 2048 {
		return ErrHTTPURL
	}
	parsed, err := url.Parse(trimmedURL)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return ErrHTTPURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrHTTPURL
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = "GET"
	}
	if method != "GET" && method != "HEAD" {
		return ErrHTTPMethod
	}
	if _, _, err := ParseExpectedStatus(cfg.ExpectedStatus); err != nil {
		return ErrHTTPStatus
	}
	if utf8.RuneCountInString(cfg.Keyword) > 500 {
		return ErrHTTPKeyword
	}
	if method == "HEAD" && cfg.Keyword != "" {
		return ErrHTTPKeyword
	}
	if cfg.CertCheck || cfg.DomainCheck {
		if parsed.Scheme != "https" {
			if cfg.CertCheck {
				return ErrHTTPSCert
			}
			return ErrDomainCheck
		}
	}
	if cfg.CertCheck && (cfg.CertWarnDays < 1 || cfg.CertWarnDays > 365) {
		return ErrHTTPSCert
	}
	if cfg.DomainCheck {
		hostname := parsed.Hostname()
		if cfg.DomainWarnDays < 1 || cfg.DomainWarnDays > 365 {
			return ErrDomainCheck
		}
		if net.ParseIP(hostname) != nil || !strings.Contains(hostname, ".") {
			return ErrDomainCheck
		}
	}
	return nil
}

func validateTCPConfig(cfg TCPConfig) error {
	host := strings.TrimSpace(cfg.Host)
	if host == "" || len(host) > 255 {
		return ErrTCPHost
	}
	if strings.IndexFunc(host, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return ErrTCPHost
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return ErrTCPPort
	}
	return nil
}
