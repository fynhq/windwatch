package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

func createFormatChannel(t *testing.T, service *Service, st *store.Store, name string, format model.WebhookFormat, webhookURL, plainSecret string) model.NotificationChannel {
	t.Helper()
	channel := model.NotificationChannel{
		Name:          name,
		Type:          model.NotificationChannelWebhook,
		Enabled:       true,
		WebhookURL:    webhookURL,
		WebhookFormat: format,
	}
	if plainSecret != "" {
		encrypted, err := service.cipher.Encrypt(plainSecret)
		if err != nil {
			t.Fatalf("encrypt channel secret: %v", err)
		}
		channel.SecretEnc = encrypted
	}
	id, err := st.CreateNotificationChannel(t.Context(), channel, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	channel.ID = id
	return channel
}

func TestWebhookPlatformSendAndSignature(t *testing.T) {
	service, st := testNotifyService(t)
	type captured struct {
		path   string
		query  url.Values
		header http.Header
		body   map[string]any
	}
	requests := make(chan captured, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode %s body: %v", r.URL.Path, err)
		}
		requests <- captured{path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone(), body: body}
		switch r.URL.Path {
		case "/dingtalk", "/wecom":
			_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		case "/feishu":
			_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	channels := []model.NotificationChannel{
		createFormatChannel(t, service, st, "DingTalk hook", model.WebhookFormatDingTalk, server.URL+"/dingtalk?access_token=token-value", "ding-secret"),
		createFormatChannel(t, service, st, "WeCom hook", model.WebhookFormatWeCom, server.URL+"/wecom?key=wecom-key", "unused-secret"),
		createFormatChannel(t, service, st, "Feishu hook", model.WebhookFormatFeishu, server.URL+"/feishu", "feishu-secret"),
	}
	notification := model.Notification{
		Kind: model.EventUp, Status: model.StatusUp, MonitorID: 1, MonitorName: "Example",
		MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP, OccurredAt: time.Now().UTC(),
	}
	for _, channel := range channels {
		body, err := service.webhookBody(channel, notification, "alert text")
		if err != nil {
			t.Fatalf("webhookBody %s: %v", channel.WebhookFormat, err)
		}
		if err := service.sendWebhookBody(t.Context(), channel, body); err != nil {
			t.Fatalf("sendWebhookBody %s: %v", channel.WebhookFormat, err)
		}
	}

	byPath := make(map[string]captured, len(channels))
	for range channels {
		request := <-requests
		byPath[request.path] = request
	}

	dingtalk := byPath["/dingtalk"]
	if dingtalk.body["msgtype"] != "text" {
		t.Fatalf("dingtalk payload = %+v", dingtalk.body)
	}
	dingtalkText, _ := dingtalk.body["text"].(map[string]any)
	if dingtalkText["content"] != "alert text" {
		t.Fatalf("dingtalk text = %+v", dingtalk.body)
	}
	if dingtalk.query.Get("access_token") != "token-value" || dingtalk.query.Get("timestamp") == "" || dingtalk.query.Get("sign") == "" {
		t.Fatalf("dingtalk query = %v", dingtalk.query)
	}
	dingMac := hmac.New(sha256.New, []byte("ding-secret"))
	dingMac.Write([]byte(dingtalk.query.Get("timestamp") + "\n" + "ding-secret"))
	if want := base64.StdEncoding.EncodeToString(dingMac.Sum(nil)); dingtalk.query.Get("sign") != want {
		t.Fatalf("dingtalk sign = %q, want %q", dingtalk.query.Get("sign"), want)
	}
	if dingtalk.header.Get("X-WindWatch-Signature") != "" {
		t.Fatal("dingtalk request kept the generic HMAC header")
	}

	wecom := byPath["/wecom"]
	if wecom.body["msgtype"] != "text" {
		t.Fatalf("wecom payload = %+v", wecom.body)
	}
	wecomText, _ := wecom.body["text"].(map[string]any)
	if wecomText["content"] != "alert text" || wecom.query.Get("sign") != "" || wecom.header.Get("X-WindWatch-Signature") != "" {
		t.Fatalf("wecom request = %+v", wecom)
	}

	feishu := byPath["/feishu"]
	if feishu.body["msg_type"] != "text" {
		t.Fatalf("feishu payload = %+v", feishu.body)
	}
	feishuContent, _ := feishu.body["content"].(map[string]any)
	timestamp, _ := feishu.body["timestamp"].(string)
	sign, _ := feishu.body["sign"].(string)
	if feishuContent["text"] != "alert text" || timestamp == "" || sign == "" {
		t.Fatalf("feishu payload = %+v", feishu.body)
	}
	feishuMac := hmac.New(sha256.New, []byte(timestamp+"\n"+"feishu-secret"))
	if want := base64.StdEncoding.EncodeToString(feishuMac.Sum(nil)); sign != want {
		t.Fatalf("feishu sign = %q, want %q", sign, want)
	}
	if feishu.header.Get("X-WindWatch-Signature") != "" {
		t.Fatal("feishu request kept the generic HMAC header")
	}
}

func TestWebhookPlatformRejectsErrorResponses(t *testing.T) {
	cases := []struct {
		name     string
		format   model.WebhookFormat
		response string
		want     string
	}{
		{"dingtalk", model.WebhookFormatDingTalk, `{"errcode":310000,"errmsg":"keywords not in content"}`, "310000"},
		{"wecom", model.WebhookFormatWeCom, `{"errcode":93000,"errmsg":"invalid webhook url"}`, "93000"},
		{"feishu", model.WebhookFormatFeishu, `{"code":19021,"msg":"sign match fail"}`, "19021"},
		{"feishu legacy", model.WebhookFormatFeishu, `{"StatusCode":9499,"StatusMessage":"Bad Request"}`, "9499"},
		{"invalid json", model.WebhookFormatDingTalk, `not-json`, "invalid webhook response"},
		{"missing code", model.WebhookFormatFeishu, `{}`, "invalid webhook response"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, st := testNotifyService(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			channel := createFormatChannel(t, service, st, "Hook", test.format, server.URL, "")
			body, err := service.webhookBody(channel, model.Notification{
				Kind: model.EventDown, Status: model.StatusDown, MonitorID: 1,
				OccurredAt: time.Now().UTC(),
			}, "alert text")
			if err != nil {
				t.Fatalf("webhookBody: %v", err)
			}
			err = service.sendWebhookBody(t.Context(), channel, body)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("sendWebhookBody error = %v, want %q", err, test.want)
			}
			var permanent permanentError
			if !errors.As(err, &permanent) {
				t.Fatalf("platform error should not retry: %v", err)
			}
		})
	}
}
