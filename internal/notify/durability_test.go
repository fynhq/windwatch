package notify

import (
	"strconv"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestTelegramSecretFailureLeavesFailedLog(t *testing.T) {
	service, st := testNotifyService(t)
	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name:            "Broken secret",
		Type:            model.NotificationChannelTelegram,
		Enabled:         true,
		TelegramChatIDs: "111",
		SecretEnc:       "v1:not-base64",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	monitorID := createTestMonitor(t, st, channelID)
	notification := model.Notification{
		Key: "down:" + strconv.FormatInt(monitorID, 10) + ":1", Kind: model.EventDown, MonitorID: monitorID,
		MonitorName: "Broken", MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, Message: "boom", OccurredAt: time.Now().UTC(),
	}
	if err := service.dispatch(t.Context(), notification); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	logs, err := st.RecentNotifications(t.Context(), 10)
	if err != nil {
		t.Fatalf("RecentNotifications: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("notify_log rows = %d, want 1 durable failed row", len(logs))
	}
	if logs[0].Status != "failed" || logs[0].Error == "" {
		t.Fatalf("failed log = %+v", logs[0])
	}
}
