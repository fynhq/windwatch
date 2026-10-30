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
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/proxy"
	"github.com/fynhq/windwatch/internal/store"
)

const (
	defaultRDAPFallbackEndpoint = "https://rdap.org/domain/"
	defaultRDAPBootstrapURL     = "https://data.iana.org/rdap/dns.json"
	rdapBootstrapTTL            = 24 * time.Hour
	rdapBootstrapRetryInterval  = 5 * time.Minute
)

// RDAP redirectors and registry edges occasionally reset connections or return
// transient 5xx responses; retry those before caching an error for the UI.
var (
	rdapRetryDelays      = []time.Duration{2 * time.Second, 4 * time.Second}
	rdapFallbackEndpoint = defaultRDAPFallbackEndpoint
	rdapBootstrapURL     = defaultRDAPBootstrapURL
	rdapBootstrap        rdapBootstrapCache
)

var ErrNoDomain = errors.New("URL does not contain a public domain")

type rdapBootstrapCache struct {
	mu          sync.Mutex
	services    map[string][]string
	fetchedAt   time.Time
	attemptedAt time.Time
}

func (c *rdapBootstrapCache) snapshot() (map[string][]string, time.Time, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.services, c.fetchedAt, c.attemptedAt
}

func (c *rdapBootstrapCache) markAttempt() {
	c.mu.Lock()
	c.attemptedAt = time.Now()
	c.mu.Unlock()
}

func (c *rdapBootstrapCache) store(services map[string][]string) {
	c.mu.Lock()
	c.services = services
	c.fetchedAt = time.Now()
	c.attemptedAt = c.fetchedAt
	c.mu.Unlock()
}

func (c *rdapBootstrapCache) reset() {
	c.mu.Lock()
	c.services = nil
	c.fetchedAt = time.Time{}
	c.attemptedAt = time.Time{}
	c.mu.Unlock()
}

// transientError marks a failure that is worth retrying.
type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }

func (e transientError) Unwrap() error { return e.err }

func isTransientError(err error) bool {
	var target transientError
	return errors.As(err, &target)
}

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

// Refresh updates cached registration data for enabled domain checks.
// retry reports that at least one lookup failed for a transient reason and
// should be tried again before the regular refresh interval.
func Refresh(ctx context.Context, st *store.Store, monitors []model.Monitor, resolver func(*int64) (*proxy.Config, error), now time.Time) (retry bool, err error) {
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

	failures := make([]error, 0, len(unique))
	for domain, monitor := range unique {
		info, fetchErr := lookupRDAP(ctx, domain, func() (*proxy.Config, error) {
			if resolver == nil {
				return nil, nil
			}
			return resolver(monitor.ProxyID)
		}, now)
		if fetchErr != nil {
			if isTransientError(fetchErr) {
				retry = true
			}
			failures = append(failures, fmt.Errorf("%s: %w", domain, fetchErr))
			previous, previousErr := st.DomainInfo(ctx, domain)
			if previousErr == nil {
				info = previous
			}
			info.Domain = domain
			info.FetchedAt = now
			info.Error = truncateRDAPError(fetchErr.Error())
		}
		if upsertErr := st.UpsertDomainInfo(ctx, info); upsertErr != nil {
			return retry, fmt.Errorf("store domain info for %s: %w", domain, upsertErr)
		}
	}
	if len(failures) > 0 {
		return retry, fmt.Errorf("refresh domain info: %w", errors.Join(failures...))
	}
	return false, nil
}

func lookupRDAP(ctx context.Context, domain string, resolve func() (*proxy.Config, error), now time.Time) (model.DomainInfo, error) {
	client, closeIdle, err := newRDAPClient(resolve)
	if err != nil {
		return model.DomainInfo{}, err
	}
	defer closeIdle()
	return fetchRDAPFrom(ctx, client, rdapEndpoints(ctx, client, domain), domain, now)
}

func fetchRDAP(ctx context.Context, endpoint, domain string, resolve func() (*proxy.Config, error), now time.Time) (model.DomainInfo, error) {
	client, closeIdle, err := newRDAPClient(resolve)
	if err != nil {
		return model.DomainInfo{}, err
	}
	defer closeIdle()
	return fetchRDAPFrom(ctx, client, []string{endpoint}, domain, now)
}

func newRDAPClient(resolve func() (*proxy.Config, error)) (*http.Client, func(), error) {
	egress, err := resolve()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve proxy: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// RDAP must follow the same direct/proxy choice as the monitor; do not
	// inherit HTTP_PROXY/HTTPS_PROXY from the process environment.
	transport.Proxy = nil
	if err := proxy.ApplyHTTPTransport(egress, transport); err != nil {
		return nil, nil, err
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}, transport.CloseIdleConnections, nil
}

func fetchRDAPFrom(ctx context.Context, client *http.Client, endpoints []string, domain string, now time.Time) (model.DomainInfo, error) {
	var failures []error
	for _, endpoint := range endpoints {
		info, err := fetchRDAPEndpoint(ctx, client, endpoint, domain, now)
		if err == nil {
			return info, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", rdapEndpointHost(endpoint), err))
		if !isTransientError(err) {
			return model.DomainInfo{}, failures[len(failures)-1]
		}
	}
	if len(failures) == 0 {
		return model.DomainInfo{}, errors.New("no RDAP endpoint available")
	}
	return model.DomainInfo{}, errors.Join(failures...)
}

func fetchRDAPEndpoint(ctx context.Context, client *http.Client, endpoint, domain string, now time.Time) (model.DomainInfo, error) {
	var lastErr error
	for attempt := 0; attempt <= len(rdapRetryDelays); attempt++ {
		if attempt > 0 {
			if waitErr := waitRetry(ctx, rdapRetryDelays[attempt-1]); waitErr != nil {
				return model.DomainInfo{}, lastErr
			}
		}
		info, requestErr := requestRDAP(ctx, client, endpoint, domain, now)
		if requestErr == nil {
			return info, nil
		}
		lastErr = requestErr
		if !isTransientError(requestErr) {
			return model.DomainInfo{}, requestErr
		}
	}
	return model.DomainInfo{}, lastErr
}

func rdapEndpointHost(endpoint string) string {
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return endpoint
}

func rdapEndpoints(ctx context.Context, client *http.Client, domain string) []string {
	services, fetchedAt, attemptedAt := rdapBootstrap.snapshot()
	stale := services == nil || time.Since(fetchedAt) > rdapBootstrapTTL
	if stale && (attemptedAt.IsZero() || time.Since(attemptedAt) >= rdapBootstrapRetryInterval) {
		rdapBootstrap.markAttempt()
		for attempt := 0; attempt <= len(rdapRetryDelays); attempt++ {
			if attempt > 0 {
				if waitErr := waitRetry(ctx, rdapRetryDelays[attempt-1]); waitErr != nil {
					break
				}
			}
			fresh, err := fetchRDAPBootstrap(ctx, client)
			if err == nil {
				rdapBootstrap.store(fresh)
				services = fresh
				break
			}
		}
	}
	tld := rdapTLD(domain)
	seen := make(map[string]struct{}, len(services[tld])+1)
	endpoints := make([]string, 0, len(services[tld])+1)
	add := func(endpoint string) {
		if endpoint == "" {
			return
		}
		if _, exists := seen[endpoint]; exists {
			return
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	for _, base := range services[tld] {
		add(rdapEndpoint(base))
	}
	add(rdapFallbackEndpoint)
	return endpoints
}

func rdapTLD(domain string) string {
	if index := strings.LastIndexByte(domain, '.'); index >= 0 {
		return domain[index+1:]
	}
	return domain
}

func rdapEndpoint(base string) string {
	return strings.TrimRight(base, "/") + "/domain/"
}

func fetchRDAPBootstrap(ctx context.Context, client *http.Client) (map[string][]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rdapBootstrapURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "WindWatch-Monitor")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("RDAP bootstrap request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, response.Body)
		return nil, fmt.Errorf("RDAP bootstrap returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read RDAP bootstrap: %w", err)
	}
	var payload struct {
		Services [][][]string `json:"services"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode RDAP bootstrap: %w", err)
	}
	services := make(map[string][]string)
	for _, service := range payload.Services {
		if len(service) != 2 {
			continue
		}
		var bases []string
		for _, raw := range service[1] {
			parsed, err := url.Parse(raw)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				continue
			}
			bases = append(bases, raw)
		}
		if len(bases) == 0 {
			continue
		}
		for _, tld := range service[0] {
			tld = strings.ToLower(strings.TrimSpace(tld))
			if tld != "" {
				services[tld] = append(services[tld], bases...)
			}
		}
	}
	if len(services) == 0 {
		return nil, errors.New("RDAP bootstrap has no HTTPS services")
	}
	return services, nil
}

func requestRDAP(ctx context.Context, client *http.Client, endpoint, domain string, now time.Time) (model.DomainInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/"+url.PathEscape(domain), nil)
	if err != nil {
		return model.DomainInfo{}, err
	}
	request.Header.Set("User-Agent", "WindWatch-Monitor")
	request.Header.Set("Accept", "application/rdap+json")
	response, err := client.Do(request)
	if err != nil {
		return model.DomainInfo{}, transientError{fmt.Errorf("RDAP request: %w", err)}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, response.Body)
		err := fmt.Errorf("RDAP returned HTTP %d", response.StatusCode)
		if retryableStatus(response.StatusCode) {
			return model.DomainInfo{}, transientError{err}
		}
		return model.DomainInfo{}, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return model.DomainInfo{}, transientError{fmt.Errorf("read RDAP response: %w", err)}
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

func retryableStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
