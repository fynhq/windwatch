package checker

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	outboundproxy "github.com/fynhq/windwatch/internal/proxy"
)

type tcpChecker struct{}

func (tcpChecker) Type() model.MonitorType {
	return model.MonitorTypeTCP
}

func (tcpChecker) Check(ctx context.Context, request Request) (model.CheckResult, error) {
	monitor := request.Monitor
	if monitor.TCP == nil {
		return failure(model.ErrorKindInvalidConfig, "missing TCP configuration"), nil
	}
	host := strings.Trim(strings.TrimSpace(monitor.TCP.Host), "[]")
	address := net.JoinHostPort(host, strconv.Itoa(monitor.TCP.Port))

	dialContext, err := outboundproxy.DialContext(request.Proxy)
	if err != nil {
		return failure(model.ErrorKindInvalidConfig, err.Error()), nil
	}
	start := time.Now()
	connection, err := dialContext(ctx, "tcp", address)
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
	if err := connection.Close(); err != nil {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    model.ErrorKindNetwork,
			ErrorMessage: truncateMessage(err.Error()),
			MetaJSON:     "{}",
		}, nil
	}
	return model.CheckResult{
		Success:  true,
		Latency:  time.Since(start),
		MetaJSON: "{}",
	}, nil
}
