package domains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/proxy"
	"github.com/fynhq/windwatch/internal/store"
)

const defaultRDAPBaseURL = "https://rdap.org/domain/"

var ErrNoDomain = errors.New("URL does not contain a public domain")

type RDAPResult struct {
	Registrar string
	Statuses  []string
	ExpiresAt time.Time
}

func DomainForURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return "", ErrNoDomain
	}
	host := strings.TrimSuffix(parsed.Hostname(), ".")
	if net.ParseIP(host) != nil {
		return "", ErrNoDomain
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", fmt.Errorf("convert domain to ASCII: %w", err)
	}
	domain, err := publicsuffix.EffectiveTLDPlusOne(ascii)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoDomain, err)
	}
	return strings.ToLower(domain), nil
}

func Refresh(ctx context.Context, st *store.Store, monitors []model.Monitor, resolver func(*int64) (*proxy.Config, error), now time.Time) error {
	unique := make(map[string]model.Monitor)
	for _, monitor := range monitors {
		if !monitor.Enabled || monitor.Type != model.MonitorTypeHTTP || monitor.HTTP == nil || !monitor.HTTP.DomainCheck {
			continue
		}
		domain, err := DomainForURL(monitor.HTTP.URL)
		if err != nil {
			continue
		}
		if _, exists := unique[domain]; !exists {
			unique[domain] = monitor
		}
	}

	for domain, monitor := range unique {
		info, err := fetchRDAP(ctx, defaultRDAPBaseURL, domain, func() (*proxy.Config, error) {
			if resolver == nil {
				return nil, nil
			}
			return resolver(monitor.ProxyID)
		}, now)
		if err != nil {
			previous, previousErr := st.DomainInfo(ctx, domain)
			if previousErr == nil {
				info = previous
			}
			info.Domain = domain
			info.FetchedAt = now
			info.Error = truncateRDAPError(err.Error())
		}
		if upsertErr := st.UpsertDomainInfo(ctx, info); upsertErr != nil {
			return fmt.Errorf("store domain info for %s: %w", domain, upsertErr)
		}
	}
	return nil
}

func fetchRDAP(ctx context.Context, baseURL, domain string, resolve func() (*proxy.Config, error), now time.Time) (model.DomainInfo, error) {
	egress, err := resolve()
	if err != nil {
		return model.DomainInfo{}, fmt.Errorf("resolve proxy: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// RDAP must follow the same direct/proxy choice as the monitor; do not
	// inherit HTTP_PROXY/HTTPS_PROXY from the process environment.
	transport.Proxy = nil
	if err := proxy.ApplyHTTPTransport(egress, transport); err != nil {
		return model.DomainInfo{}, err
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/"+url.PathEscape(domain), nil)
	if err != nil {
		return model.DomainInfo{}, err
	}
	request.Header.Set("User-Agent", "WindWatch-Monitor")
	response, err := client.Do(request)
	if err != nil {
		return model.DomainInfo{}, fmt.Errorf("RDAP request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, response.Body)
		return model.DomainInfo{}, fmt.Errorf("RDAP returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return model.DomainInfo{}, fmt.Errorf("read RDAP response: %w", err)
	}
	result, err := parseRDAP(data)
	if err != nil {
		return model.DomainInfo{}, err
	}
	return model.DomainInfo{
		Domain:    domain,
		Registrar: result.Registrar,
		Statuses:  result.Statuses,
		ExpiresAt: result.ExpiresAt,
		FetchedAt: now,
	}, nil
}

func parseRDAP(data []byte) (RDAPResult, error) {
	var payload struct {
		Status []string `json:"status"`
		Events []struct {
			Action string `json:"eventAction"`
			Date   string `json:"eventDate"`
		} `json:"events"`
		Entities []struct {
			Roles      []string          `json:"roles"`
			VCardArray []json.RawMessage `json:"vcardArray"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return RDAPResult{}, fmt.Errorf("decode RDAP response: %w", err)
	}

	result := RDAPResult{Statuses: payload.Status}
	for _, event := range payload.Events {
		if !strings.EqualFold(event.Action, "expiration") {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, event.Date)
		if err != nil {
			return RDAPResult{}, fmt.Errorf("parse RDAP expiration: %w", err)
		}
		result.ExpiresAt = parsed.UTC()
		break
	}
	if result.ExpiresAt.IsZero() {
		return RDAPResult{}, errors.New("RDAP response has no expiration event")
	}

	for _, entity := range payload.Entities {
		if !containsRole(entity.Roles, "registrar") {
			continue
		}
		result.Registrar = vcardName(entity.VCardArray)
		break
	}
	return result, nil
}

func truncateRDAPError(value string) string {
	const limit = 512
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func containsRole(roles []string, want string) bool {
	for _, role := range roles {
		if strings.EqualFold(role, want) {
			return true
		}
	}
	return false
}

func vcardName(raw []json.RawMessage) string {
	if len(raw) < 2 {
		return ""
	}
	var entries []any
	if err := json.Unmarshal(raw[1], &entries); err != nil {
		return ""
	}
	for _, entry := range entries {
		fields, ok := entry.([]any)
		if !ok || len(fields) < 4 {
			continue
		}
		key, _ := fields[0].(string)
		if key != "fn" {
			continue
		}
		value, _ := fields[3].(string)
		return value
	}
	return ""
}

// HasSuspensionStatus reports RDAP statuses that make a domain unusable.
func HasSuspensionStatus(statuses []string) bool {
	for _, status := range statuses {
		switch strings.ToLower(status) {
		case "clienthold", "serverhold", "pendingdelete", "redemptionperiod":
			return true
		}
	}
	return false
}
