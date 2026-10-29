package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	netproxy "golang.org/x/net/proxy"

	"github.com/fynhq/windwatch/internal/model"
)

type Config struct {
	Type     model.ProxyType
	Address  string
	Username string
	Password string
}

var (
	ErrInvalidConfig = errors.New("invalid proxy configuration")
	ErrProxyFailure  = errors.New("proxy request failed")
)

func wrapProxyError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrProxyFailure, err)
}

func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if c.Type != model.ProxyHTTP && c.Type != model.ProxySOCKS5 {
		return ErrInvalidConfig
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(c.Address))
	if err != nil || host == "" || port == "" {
		return ErrInvalidConfig
	}
	return nil
}

func ApplyHTTPTransport(cfg *Config, transport *http.Transport) error {
	if cfg == nil {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	switch cfg.Type {
	case model.ProxyHTTP:
		proxyURL := &url.URL{Scheme: "http", Host: cfg.Address}
		if cfg.Username != "" {
			proxyURL.User = url.UserPassword(cfg.Username, cfg.Password)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
		baseDial := transport.DialContext
		if baseDial == nil {
			baseDial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			connection, err := baseDial(ctx, network, address)
			if err != nil {
				return nil, wrapProxyError(err)
			}
			return connection, nil
		}
		return nil
	case model.ProxySOCKS5:
		dialContext, err := DialContext(cfg)
		if err != nil {
			return err
		}
		transport.Proxy = nil
		transport.DialContext = dialContext
		return nil
	default:
		return ErrInvalidConfig
	}
}

func DialContext(cfg *Config) (func(context.Context, string, string) (net.Conn, error), error) {
	if cfg == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Type {
	case model.ProxySOCKS5:
		var auth *netproxy.Auth
		if cfg.Username != "" {
			auth = &netproxy.Auth{User: cfg.Username, Password: cfg.Password}
		}
		dialer, err := netproxy.SOCKS5("tcp", cfg.Address, auth, &net.Dialer{Timeout: 10 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("create SOCKS5 dialer: %w", err)
		}
		if contextDialer, ok := dialer.(netproxy.ContextDialer); ok {
			return func(ctx context.Context, network, address string) (net.Conn, error) {
				connection, err := contextDialer.DialContext(ctx, network, address)
				if err != nil {
					return nil, wrapProxyError(err)
				}
				return connection, nil
			}, nil
		}
		return func(ctx context.Context, network, address string) (net.Conn, error) {
			type result struct {
				conn net.Conn
				err  error
			}
			results := make(chan result, 1)
			go func() {
				conn, err := dialer.Dial(network, address)
				results <- result{conn: conn, err: err}
			}()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case result := <-results:
				if result.err != nil {
					return nil, wrapProxyError(result.err)
				}
				return result.conn, nil
			}
		}, nil
	case model.ProxyHTTP:
		return func(ctx context.Context, network, address string) (net.Conn, error) {
			connection, err := dialHTTPConnect(ctx, cfg, network, address)
			if err != nil {
				return nil, wrapProxyError(err)
			}
			return connection, nil
		}, nil
	default:
		return nil, ErrInvalidConfig
	}
}

func CheckConnectivity(ctx context.Context, cfg *Config, target string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dial, err := DialContext(cfg)
	if err != nil {
		return err
	}
	connection, err := dial(ctx, "tcp", target)
	if err != nil {
		return err
	}
	return connection.Close()
}

func dialHTTPConnect(ctx context.Context, cfg *Config, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("connect to proxy: %w", err)
	}
	success := false
	defer func() {
		if !success {
			connection.Close()
		}
	}()

	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	var request strings.Builder
	request.WriteString("CONNECT ")
	request.WriteString(address)
	request.WriteString(" HTTP/1.1\r\nHost: ")
	request.WriteString(address)
	request.WriteString("\r\n")
	if cfg.Username != "" {
		credentials := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
		request.WriteString("Proxy-Authorization: Basic ")
		request.WriteString(credentials)
		request.WriteString("\r\n")
	}
	request.WriteString("\r\n")
	if _, err := connection.Write([]byte(request.String())); err != nil {
		return nil, fmt.Errorf("write proxy CONNECT: %w", err)
	}

	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, fmt.Errorf("read proxy CONNECT response: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("proxy CONNECT returned HTTP %d", response.StatusCode)
	}
	_ = connection.SetDeadline(time.Time{})
	success = true
	return &bufferedConn{Conn: connection, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}
