package model

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type ProxyType string

const (
	ProxyHTTP   ProxyType = "http"
	ProxySOCKS5 ProxyType = "socks5"
)

type Proxy struct {
	ID          int64
	Name        string
	Type        ProxyType
	Address     string
	Username    string
	PasswordEnc string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

var (
	ErrProxyName    = errors.New("invalid proxy name")
	ErrProxyType    = errors.New("invalid proxy type")
	ErrProxyAddress = errors.New("invalid proxy address")
)

func (p Proxy) Validate() error {
	if strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(strings.TrimSpace(p.Name)) > 100 {
		return ErrProxyName
	}
	if p.Type != ProxyHTTP && p.Type != ProxySOCKS5 {
		return ErrProxyType
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(p.Address))
	if err != nil || strings.TrimSpace(host) == "" {
		return ErrProxyAddress
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return ErrProxyAddress
	}
	return nil
}
