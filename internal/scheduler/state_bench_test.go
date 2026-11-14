package scheduler

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

var (
	benchState model.MonitorState
	benchEvent *model.Event
	benchClose bool
)

func BenchmarkAdvance(b *testing.B) {
	monitor := model.Monitor{
		ID:               1,
		Type:             model.MonitorTypeHTTP,
		HTTP:             &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval:         60 * time.Second,
		Timeout:          10 * time.Second,
		FailThreshold:    2,
		RecoverThreshold: 1,
	}
	state := model.MonitorState{Status: model.StatusUp, StatusSince: time.Now().UTC()}
	success := model.CheckResult{Success: true, Latency: 20 * time.Millisecond, HTTPStatus: 200}
	failure := model.CheckResult{Success: false, Latency: 20 * time.Millisecond, ErrorKind: model.ErrorKindNetwork, ErrorMessage: "connection refused"}
	at := time.Now().UTC()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at = at.Add(time.Minute)
		result := success
		if i%2 == 1 {
			result = failure
		}
		benchState, benchEvent, benchClose = advance(monitor, state, result, at)
	}
}
