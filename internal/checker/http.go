package checker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/proxy"
)

const (
	maxDiscardBodyBytes = 64 << 10
	maxKeywordBodyBytes = 1 << 20
	maxRedirects        = 5
)

var errTooManyRedirects = errors.New("too many redirects")

type httpChecker struct {
	transport *http.Transport
}

type httpMeta struct {
	FinalURL string `json:"final_url,omitempty"`
}

func newHTTPChecker() *httpChecker {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &httpChecker{transport: transport}
}

func (c *httpChecker) Type() model.MonitorType {
	return model.MonitorTypeHTTP
}

func (c *httpChecker) Check(ctx context.Context, request Request) (model.CheckResult, error) {
	monitor := request.Monitor
	if monitor.HTTP == nil {
		return failure(model.ErrorKindInvalidConfig, "missing HTTP configuration"), nil
	}
	cfg := *monitor.HTTP
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSpace(cfg.URL), nil)
	if err != nil {
		return failure(model.ErrorKindInvalidConfig, err.Error()), nil
	}
	req.Header.Set("User-Agent", "WindWatch-Monitor")

	transport := c.transport.Clone()
	if cfg.TLSSkipVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	if err := proxy.ApplyHTTPTransport(request.Proxy, transport); err != nil {
		return failure(model.ErrorKindInvalidConfig, err.Error()), nil
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport:     transport,
		CheckRedirect: redirectPolicy(cfg.FollowRedirects),
	}
	start := time.Now()
	response, err := client.Do(req)
	if err != nil {
		kind, message := classifyError(err)
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    kind,
			ErrorMessage: message,
			MetaJSON:     "{}",
		}, nil
	}
	defer response.Body.Close()

	metaJSON := encodeHTTPMeta(response.Request)
	certificate := certificateFromTLS(response.TLS)
	if request.Proxy != nil && response.StatusCode == http.StatusProxyAuthRequired {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			HTTPStatus:   response.StatusCode,
			ErrorKind:    model.ErrorKindProxy,
			ErrorMessage: "proxy authentication required",
			MetaJSON:     metaJSON,
			Certificate:  certificate,
		}, nil
	}
	if !model.StatusMatches(cfg.ExpectedStatus, response.StatusCode) {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			HTTPStatus:   response.StatusCode,
			ErrorKind:    model.ErrorKindInvalidStatus,
			ErrorMessage: fmt.Sprintf("unexpected HTTP status %d", response.StatusCode),
			MetaJSON:     metaJSON,
			Certificate:  certificate,
		}, nil
	}

	if method != http.MethodHead {
		if cfg.Keyword != "" {
			body, err := io.ReadAll(io.LimitReader(response.Body, maxKeywordBodyBytes+1))
			if err != nil {
				kind, message := classifyError(err)
				return model.CheckResult{
					Success:      false,
					Latency:      time.Since(start),
					HTTPStatus:   response.StatusCode,
					ErrorKind:    kind,
					ErrorMessage: message,
					MetaJSON:     metaJSON,
					Certificate:  certificate,
				}, nil
			}
			if !strings.Contains(string(body), cfg.Keyword) {
				message := "expected keyword was not found"
				if len(body) > maxKeywordBodyBytes {
					message = "expected keyword was not found within the first 1 MiB of the response"
				}
				return model.CheckResult{
					Success:      false,
					Latency:      time.Since(start),
					HTTPStatus:   response.StatusCode,
					ErrorKind:    model.ErrorKindKeywordMissing,
					ErrorMessage: message,
					MetaJSON:     metaJSON,
					Certificate:  certificate,
				}, nil
			}
		} else {
			// Read a bounded prefix so the connection can be reused without
			// letting large responses consume memory.
			if _, err := io.CopyN(io.Discard, response.Body, maxDiscardBodyBytes); err != nil && !errors.Is(err, io.EOF) {
				kind, message := classifyError(err)
				return model.CheckResult{
					Success:      false,
					Latency:      time.Since(start),
					HTTPStatus:   response.StatusCode,
					ErrorKind:    kind,
					ErrorMessage: message,
					MetaJSON:     metaJSON,
					Certificate:  certificate,
				}, nil
			}
		}
	}

	return model.CheckResult{
		Success:     true,
		Latency:     time.Since(start),
		HTTPStatus:  response.StatusCode,
		MetaJSON:    metaJSON,
		Certificate: certificate,
	}, nil
}

func certificateFromTLS(state *tls.ConnectionState) *model.CertificateInfo {
	if state == nil || len(state.PeerCertificates) == 0 {
		return nil
	}
	leaf := state.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	sans := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses))
	sans = append(sans, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		sans = append(sans, ip.String())
	}
	return &model.CertificateInfo{
		Fingerprint: hex.EncodeToString(sum[:]),
		Subject:     leaf.Subject.String(),
		Issuer:      leaf.Issuer.String(),
		SANs:        sans,
		NotBefore:   leaf.NotBefore,
		NotAfter:    leaf.NotAfter,
	}
}

func redirectPolicy(follow bool) func(*http.Request, []*http.Request) error {
	if !follow {
		return func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return func(_ *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errTooManyRedirects
		}
		return nil
	}
}

func encodeHTTPMeta(request *http.Request) string {
	if request == nil || request.URL == nil {
		return "{}"
	}
	data, err := json.Marshal(httpMeta{FinalURL: request.URL.String()})
	if err != nil {
		return "{}"
	}
	return string(data)
}
