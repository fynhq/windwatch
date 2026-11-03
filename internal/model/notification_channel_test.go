package model

import "testing"

func TestWebhookNotificationTargetHidesPathAndQuery(t *testing.T) {
	channel := NotificationChannel{
		Type:       NotificationChannelWebhook,
		WebhookURL: "https://hooks.example.com/services/SECRET?token=SECRET",
	}
	if got := channel.NotificationTarget(); got != "https://hooks.example.com" {
		t.Fatalf("NotificationTarget = %q, want origin only", got)
	}
}

func TestWebhookURLRejectsUserInfo(t *testing.T) {
	channel := NotificationChannel{
		Name:          "Hook",
		Type:          NotificationChannelWebhook,
		WebhookURL:    "https://user:pass@example.com/hook",
		WebhookFormat: WebhookFormatGeneric,
	}
	if err := channel.Validate(); err != ErrChannelWebhookURL {
		t.Fatalf("Validate error = %v, want ErrChannelWebhookURL", err)
	}
}
