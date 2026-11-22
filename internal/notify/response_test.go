package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

type errorReadCloser struct{}

func (errorReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errorReadCloser) Close() error             { return nil }

type staticRoundTripper struct {
	response *http.Response
}

func (r staticRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return r.response, nil
}

func TestTelegramReadErrorIsRetained(t *testing.T) {
	service := &Service{telegramBaseURL: "http://telegram.invalid"}
	client := &http.Client{Transport: staticRoundTripper{response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       errorReadCloser{},
		Header:     make(http.Header),
	}}}
	err := service.postTelegram(context.Background(), client, "token", "1", "text")
	if err == nil {
		t.Fatal("expected Telegram response read error")
	}
	var permanent permanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("error = %v, want permanentError", err)
	}
	if !strings.Contains(err.Error(), io.ErrUnexpectedEOF.Error()) {
		t.Fatalf("read error was not retained: %v", err)
	}
}

func TestWebhookReadErrorClassification(t *testing.T) {
	if err := webhookResponseError(model.WebhookFormatGeneric, nil, io.ErrUnexpectedEOF); err != nil {
		t.Fatalf("generic webhook status-only response should succeed: %v", err)
	}
	err := webhookResponseError(model.WebhookFormatDingTalk, []byte(`{"errcode":0}`), io.ErrUnexpectedEOF)
	var permanent permanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("structured webhook read error = %v, want permanentError", err)
	}
	if !strings.Contains(err.Error(), io.ErrUnexpectedEOF.Error()) {
		t.Fatalf("webhook read error was not retained: %v", err)
	}
}

func TestEarlierStateLookupFailureIsReported(t *testing.T) {
	service, _ := testNotifyService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blocked, err := service.blockedByEarlierState(ctx, model.NotificationLog{
		ID: 1, DedupeKey: "down:1:1", Channel: "telegram", ChannelID: 1, Target: "1",
		CreatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected lookup error")
	}
	if blocked {
		t.Fatal("failed lookup must not report an earlier notification")
	}
}
