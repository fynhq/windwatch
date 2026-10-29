package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/fynhq/windwatch/internal/model"
	outboundproxy "github.com/fynhq/windwatch/internal/proxy"
)

const maxErrorMessageBytes = 512

func classifyError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	message := truncateMessage(err.Error())

	if errors.Is(err, outboundproxy.ErrProxyFailure) {
		return model.ErrorKindProxy, message
	}
	lowerMessage := strings.ToLower(message)
	if strings.Contains(lowerMessage, "proxyconnect") ||
		strings.Contains(lowerMessage, "proxy authentication required") ||
		strings.Contains(lowerMessage, "socks connect") {
		return model.ErrorKindProxy, message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return model.ErrorKindTimeout, message
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return model.ErrorKindTimeout, message
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return model.ErrorKindDNS, message
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return model.ErrorKindConnectionRefused, message
	}
	var certVerifyErr *tls.CertificateVerificationError
	if errors.As(err, &certVerifyErr) {
		return model.ErrorKindTLS, message
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return model.ErrorKindTLS, message
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return model.ErrorKindTLS, message
	}
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &certificateInvalid) {
		return model.ErrorKindTLS, message
	}
	if strings.Contains(lowerMessage, "tls:") ||
		strings.Contains(lowerMessage, "x509:") ||
		strings.Contains(lowerMessage, "failed to verify certificate") ||
		strings.Contains(lowerMessage, "certificate is not valid") {
		return model.ErrorKindTLS, message
	}
	return model.ErrorKindNetwork, message
}

func truncateMessage(message string) string {
	if len(message) <= maxErrorMessageBytes {
		return message
	}
	return message[:maxErrorMessageBytes]
}
