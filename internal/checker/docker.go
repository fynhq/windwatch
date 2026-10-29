package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

type dockerChecker struct {
	client     *http.Client
	baseURL    string
	versionMu  sync.Mutex
	apiVersion string
}

type dockerInspect struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Running    bool   `json:"Running"`
		Status     string `json:"Status"`
		Restarting bool   `json:"Restarting"`
		StartedAt  string `json:"StartedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image string `json:"Image"`
	} `json:"Config"`
}

func defaultDockerHostFromEnv() string {
	if value := strings.TrimSpace(os.Getenv("DOCKER_HOST")); value != "" {
		return value
	}
	return defaultDockerHost
}

func newDockerChecker(host string) *dockerChecker {
	host = strings.TrimSpace(host)
	if host == "" {
		host = defaultDockerHost
	}

	transport := &http.Transport{}
	baseURL := "http://docker"
	switch {
	case strings.HasPrefix(host, "unix://"):
		socketPath := strings.TrimPrefix(host, "unix://")
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", socketPath)
		}
	case strings.HasPrefix(host, "tcp://"):
		address := strings.TrimPrefix(host, "tcp://")
		baseURL = "http://" + address
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
		}
	case strings.HasPrefix(host, "/"):
		socketPath := host
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", socketPath)
		}
	case strings.HasPrefix(host, "npipe://"):
		dialer, err := dockerNPipeDialer(host)
		if err != nil {
			transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
				return nil, err
			}
		} else {
			transport.DialContext = dialer
		}
	default:
		transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
			return nil, fmt.Errorf("unsupported Docker endpoint %q", host)
		}
	}

	return &dockerChecker{
		baseURL: baseURL,
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
	}
}

func (d *dockerChecker) Type() model.MonitorType {
	return model.MonitorTypeDocker
}

func (d *dockerChecker) Check(ctx context.Context, request Request) (model.CheckResult, error) {
	monitor := request.Monitor
	if monitor.Docker == nil || strings.TrimSpace(monitor.Docker.Container) == "" {
		return failure(model.ErrorKindInvalidConfig, "missing Docker configuration"), nil
	}

	start := time.Now()
	version, err := d.getAPIVersion(ctx)
	if err != nil {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    model.ErrorKindDockerUnavailable,
			ErrorMessage: truncateMessage(err.Error()),
			MetaJSON:     "{}",
		}, nil
	}

	container := strings.TrimSpace(monitor.Docker.Container)
	path := "/v" + version + "/containers/" + url.PathEscape(container) + "/json"
	var inspect dockerInspect
	statusCode, err := d.getJSON(ctx, path, &inspect)
	if err != nil {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    model.ErrorKindDockerUnavailable,
			ErrorMessage: truncateMessage(err.Error()),
			MetaJSON:     "{}",
		}, nil
	}
	meta := dockerMeta{
		ContainerID:  inspect.ID,
		Container:    strings.TrimPrefix(inspect.Name, "/"),
		Image:        inspect.Config.Image,
		RestartCount: inspect.RestartCount,
		StartedAt:    inspect.State.StartedAt,
	}
	metaJSON := encodeJSON(meta)

	if statusCode == http.StatusNotFound {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    model.ErrorKindContainerMissing,
			ErrorMessage: "container was not found",
			MetaJSON:     metaJSON,
		}, nil
	}
	if statusCode < 200 || statusCode >= 300 {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    model.ErrorKindDockerUnavailable,
			ErrorMessage: fmt.Sprintf("docker API returned HTTP %d", statusCode),
			MetaJSON:     metaJSON,
		}, nil
	}
	if !inspect.State.Running || inspect.State.Restarting {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    model.ErrorKindContainerStopped,
			ErrorMessage: "container is not running",
			MetaJSON:     metaJSON,
		}, nil
	}
	if monitor.Docker.RequireHealthy && inspect.State.Health != nil && inspect.State.Health.Status != "healthy" {
		return model.CheckResult{
			Success:      false,
			Latency:      time.Since(start),
			ErrorKind:    model.ErrorKindContainerUnhealthy,
			ErrorMessage: "container health status is " + inspect.State.Health.Status,
			MetaJSON:     metaJSON,
		}, nil
	}

	return model.CheckResult{
		Success:  true,
		Latency:  time.Since(start),
		MetaJSON: metaJSON,
	}, nil
}

type dockerMeta struct {
	ContainerID  string `json:"container_id,omitempty"`
	Container    string `json:"container,omitempty"`
	Image        string `json:"image,omitempty"`
	RestartCount int    `json:"restart_count,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
}

func (d *dockerChecker) getAPIVersion(ctx context.Context) (string, error) {
	d.versionMu.Lock()
	defer d.versionMu.Unlock()
	if d.apiVersion != "" {
		return d.apiVersion, nil
	}

	var response struct {
		APIVersion string `json:"ApiVersion"`
	}
	status, err := d.getJSON(ctx, "/version", &response)
	if err != nil {
		return "", fmt.Errorf("query Docker version: %w", err)
	}
	if status < 200 || status >= 300 || response.APIVersion == "" {
		return "", fmt.Errorf("docker version endpoint returned HTTP %d", status)
	}
	d.apiVersion = response.APIVersion
	return d.apiVersion, nil
}

func (d *dockerChecker) getJSON(ctx context.Context, path string, output any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	response, err := d.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		io.Copy(io.Discard, response.Body)
		return response.StatusCode, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, response.Body)
		return response.StatusCode, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if err := json.Unmarshal(data, output); err != nil {
		return response.StatusCode, fmt.Errorf("decode Docker response: %w", err)
	}
	return response.StatusCode, nil
}

func encodeJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}
