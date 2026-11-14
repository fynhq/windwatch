package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func startEchoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo server: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func(connection net.Conn) {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}(connection)
		}
	}()
	return listener.Addr().String()
}

func roundTrip(t *testing.T, connection net.Conn) {
	t.Helper()
	payload := []byte("windwatch-proxy-test")
	if _, err := connection.Write(payload); err != nil {
		t.Fatalf("write through proxy: %v", err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatalf("read through proxy: %v", err)
	}
	if string(response) != string(payload) {
		t.Fatalf("echo = %q, want %q", response, payload)
	}
}

func TestHTTPProxyConnectSuccess(t *testing.T) {
	target := startEchoServer(t)
	const username, password = "user", "pass"
	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Proxy-Authorization") != expectedAuth {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		target := r.Host
		if target == "" {
			target = r.URL.Host
		}
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			upstream.Close()
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		client, _, err := hijacker.Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			client.Close()
			upstream.Close()
			return
		}
		go func() {
			defer client.Close()
			defer upstream.Close()
			_, _ = io.Copy(upstream, client)
		}()
		_, _ = io.Copy(client, upstream)
	}))
	defer proxyServer.Close()

	cfg := &Config{
		Type:     model.ProxyHTTP,
		Address:  strings.TrimPrefix(proxyServer.URL, "http://"),
		Username: username,
		Password: password,
	}
	dial, err := DialContext(cfg)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, err := dial(ctx, "tcp", target)
	if err != nil {
		t.Fatalf("dial through HTTP proxy: %v", err)
	}
	defer connection.Close()
	roundTrip(t, connection)
}

func TestSOCKS5DialSuccess(t *testing.T) {
	for _, requireAuth := range []bool{false, true} {
		t.Run(fmt.Sprintf("auth=%v", requireAuth), func(t *testing.T) {
			target := startEchoServer(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen SOCKS5 proxy: %v", err)
			}
			defer listener.Close()
			go serveSOCKS5(listener, requireAuth)

			cfg := &Config{Type: model.ProxySOCKS5, Address: listener.Addr().String()}
			if requireAuth {
				cfg.Username = "user"
				cfg.Password = "pass"
			}
			dial, err := DialContext(cfg)
			if err != nil {
				t.Fatalf("DialContext: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			connection, err := dial(ctx, "tcp", target)
			if err != nil {
				t.Fatalf("dial through SOCKS5 proxy: %v", err)
			}
			defer connection.Close()
			roundTrip(t, connection)
		})
	}
}

func serveSOCKS5(listener net.Listener, requireAuth bool) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go handleSOCKS5(connection, requireAuth)
	}
}

func handleSOCKS5(connection net.Conn, requireAuth bool) {
	defer connection.Close()
	reader := bufio.NewReader(connection)

	version, err := reader.ReadByte()
	if err != nil || version != 5 {
		return
	}
	methodCount, err := reader.ReadByte()
	if err != nil {
		return
	}
	methods := make([]byte, int(methodCount))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return
	}
	method := byte(0)
	if requireAuth {
		method = 2
	}
	if _, err := connection.Write([]byte{5, method}); err != nil {
		return
	}
	if requireAuth {
		if !readSOCKS5Auth(reader) {
			_, _ = connection.Write([]byte{1, 1})
			return
		}
		if _, err := connection.Write([]byte{1, 0}); err != nil {
			return
		}
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil || header[1] != 1 {
		return
	}
	host, err := readSOCKS5Address(reader, header[3])
	if err != nil {
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return
	}
	upstream, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes)))))
	if err != nil {
		_, _ = connection.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(upstream, reader)
		if closer, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}()
	_, _ = io.Copy(connection, upstream)
}

func readSOCKS5Auth(reader *bufio.Reader) bool {
	version, err := reader.ReadByte()
	if err != nil || version != 1 {
		return false
	}
	usernameLength, err := reader.ReadByte()
	if err != nil {
		return false
	}
	username := make([]byte, int(usernameLength))
	if _, err := io.ReadFull(reader, username); err != nil {
		return false
	}
	passwordLength, err := reader.ReadByte()
	if err != nil {
		return false
	}
	password := make([]byte, int(passwordLength))
	if _, err := io.ReadFull(reader, password); err != nil {
		return false
	}
	return string(username) == "user" && string(password) == "pass"
}

func readSOCKS5Address(reader *bufio.Reader, addressType byte) (string, error) {
	switch addressType {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		return net.IP(address).String(), nil
	case 3:
		length, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		address := make([]byte, int(length))
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		return string(address), nil
	case 4:
		address := make([]byte, 16)
		if _, err := io.ReadFull(reader, address); err != nil {
			return "", err
		}
		return net.IP(address).String(), nil
	default:
		return "", fmt.Errorf("unsupported address type %d", addressType)
	}
}
