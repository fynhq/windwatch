package checker

import (
	"context"
	"fmt"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/proxy"
)

type Request struct {
	Monitor model.Monitor
	Proxy   *proxy.Config
}

type Checker interface {
	Type() model.MonitorType
	Check(ctx context.Context, request Request) (model.CheckResult, error)
}

type Registry struct {
	byType map[model.MonitorType]Checker
}

func NewRegistry() *Registry {
	return NewRegistryWithDockerHost(defaultDockerHostFromEnv())
}

func NewRegistryWithDockerHost(dockerHost string) *Registry {
	registry := &Registry{byType: make(map[model.MonitorType]Checker)}
	httpChecker := newHTTPChecker()
	registry.byType[httpChecker.Type()] = httpChecker
	tcpChecker := tcpChecker{}
	registry.byType[tcpChecker.Type()] = tcpChecker
	dockerChecker := newDockerChecker(dockerHost)
	registry.byType[dockerChecker.Type()] = dockerChecker
	return registry
}

func (r *Registry) CheckWithProxy(ctx context.Context, monitor model.Monitor, egress *proxy.Config) (model.CheckResult, error) {
	if err := monitor.Validate(); err != nil {
		return failure(model.ErrorKindInvalidConfig, err.Error()), nil
	}
	checker, ok := r.byType[monitor.Type]
	if !ok {
		return failure(model.ErrorKindInvalidConfig, fmt.Sprintf("unsupported monitor type %q", monitor.Type)), nil
	}
	return checker.Check(ctx, Request{Monitor: monitor, Proxy: egress})
}

func failure(kind, message string) model.CheckResult {
	return model.CheckResult{
		Success:      false,
		ErrorKind:    kind,
		ErrorMessage: truncateMessage(message),
		MetaJSON:     "{}",
	}
}
