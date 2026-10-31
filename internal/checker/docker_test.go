package checker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

type fakeDockerContainer struct {
	running  bool
	health   string
	restarts int
}

func fakeDockerServer(t *testing.T, containers map[string]fakeDockerContainer) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/version":
			_ = json.NewEncoder(w).Encode(map[string]string{"ApiVersion": "1.45"})
		case strings.HasPrefix(r.URL.Path, "/v1.45/containers/"):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.45/containers/"), "/json")
			container, ok := containers[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			response := map[string]any{
				"Id":           "id-" + name,
				"Name":         "/" + name,
				"RestartCount": container.restarts,
				"State": map[string]any{
					"Running": container.running,
					"Status":  "running",
				},
				"Config": map[string]any{"Image": "example/image:latest"},
			}
			if container.health != "" {
				response["State"].(map[string]any)["Health"] = map[string]string{"Status": container.health}
			}
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.NotFound(w, r)
		}
	}))
}

func dockerMonitor(container string, requireHealthy bool) model.Monitor {
	return model.Monitor{
		Name:             "Docker monitor",
		Type:             model.MonitorTypeDocker,
		Docker:           &model.DockerConfig{Container: container, RequireHealthy: requireHealthy},
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    1,
		RecoverThreshold: 1,
		Enabled:          true,
	}
}

func TestDockerChecker(t *testing.T) {
	server := fakeDockerServer(t, map[string]fakeDockerContainer{
		"healthy":   {running: true, health: "healthy"},
		"starting":  {running: true, health: "starting"},
		"unhealthy": {running: true, health: "unhealthy"},
		"stopped":   {running: false},
		"plain":     {running: true},
	})
	defer server.Close()

	registry := NewRegistryWithDockerHost("tcp://" + strings.TrimPrefix(server.URL, "http://"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := registry.CheckWithProxy(ctx, dockerMonitor("healthy", true), nil)
	if err != nil || !result.Success {
		t.Fatalf("healthy result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(ctx, dockerMonitor("starting", true), nil)
	if err != nil || !result.Success {
		t.Fatalf("starting result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(ctx, dockerMonitor("unhealthy", true), nil)
	if err != nil || result.Success || result.ErrorKind != model.ErrorKindContainerUnhealthy {
		t.Fatalf("unhealthy result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(ctx, dockerMonitor("stopped", true), nil)
	if err != nil || result.Success || result.ErrorKind != model.ErrorKindContainerStopped {
		t.Fatalf("stopped result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(ctx, dockerMonitor("plain", true), nil)
	if err != nil || !result.Success {
		t.Fatalf("plain running result=%+v err=%v", result, err)
	}

	result, err = registry.CheckWithProxy(ctx, dockerMonitor("missing", true), nil)
	if err != nil || result.Success || result.ErrorKind != model.ErrorKindContainerMissing {
		t.Fatalf("missing result=%+v err=%v", result, err)
	}
}

func TestDockerCheckerUnavailable(t *testing.T) {
	registry := NewRegistryWithDockerHost("tcp://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := registry.CheckWithProxy(ctx, dockerMonitor("anything", false), nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Success || result.ErrorKind != model.ErrorKindDockerUnavailable {
		t.Fatalf("unexpected result: %+v", result)
	}
}
