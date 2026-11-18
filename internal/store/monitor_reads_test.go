package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestMonitorReadsLoadChannelsOnlyWhenRequested(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC()
	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name:       "Ops hook",
		Type:       model.NotificationChannelWebhook,
		Enabled:    true,
		WebhookURL: "https://example.com/hook",
	}, now)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name:                   "Example",
		Type:                   model.MonitorTypeHTTP,
		HTTP:                   &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval:               60 * time.Second,
		Timeout:                10 * time.Second,
		FailThreshold:          1,
		RecoverThreshold:       1,
		Enabled:                true,
		NotificationChannelIDs: []int64{channelID},
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	monitors, err := st.ListMonitors(t.Context())
	if err != nil || len(monitors) != 1 {
		t.Fatalf("ListMonitors: %+v err=%v", monitors, err)
	}
	if len(monitors[0].NotificationChannelIDs) != 0 {
		t.Fatalf("ListMonitors loaded channels: %+v", monitors[0].NotificationChannelIDs)
	}
	config, err := st.MonitorConfigByID(t.Context(), id)
	if err != nil || len(config.NotificationChannelIDs) != 0 {
		t.Fatalf("MonitorConfigByID: %+v err=%v", config.NotificationChannelIDs, err)
	}
	full, err := st.MonitorByID(t.Context(), id)
	if err != nil || len(full.NotificationChannelIDs) != 1 || full.NotificationChannelIDs[0] != channelID {
		t.Fatalf("MonitorByID channels: %+v err=%v", full.NotificationChannelIDs, err)
	}
	if len(full.EnabledNotificationChannelIDs) != 1 || full.EnabledNotificationChannelIDs[0] != channelID {
		t.Fatalf("MonitorByID enabled channels: %+v", full.EnabledNotificationChannelIDs)
	}
	enabled, err := st.EnabledNotificationChannelIDsForMonitor(t.Context(), id)
	if err != nil || len(enabled) != 1 || enabled[0] != channelID {
		t.Fatalf("EnabledNotificationChannelIDsForMonitor: %+v err=%v", enabled, err)
	}
}

func TestDeleteMonitorDropsPendingNotificationsAndOutbox(t *testing.T) {
	st := newStoreTest(t)
	now := time.Now().UTC()
	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name:       "Ops hook",
		Type:       model.NotificationChannelWebhook,
		Enabled:    true,
		WebhookURL: "https://example.com/hook",
	}, now)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	channel, err := st.NotificationChannelByID(t.Context(), channelID)
	if err != nil {
		t.Fatalf("NotificationChannelByID: %v", err)
	}
	id, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name:                   "Example",
		Type:                   model.MonitorTypeHTTP,
		HTTP:                   &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval:               60 * time.Second,
		Timeout:                10 * time.Second,
		FailThreshold:          1,
		RecoverThreshold:       1,
		Enabled:                true,
		NotificationChannelIDs: []int64{channelID},
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	key := fmt.Sprintf("down:%d:%d", id, now.UnixMilli())
	keys := []string{
		key,
		fmt.Sprintf("warn_cert:%d:fp:14:%d", id, now.UnixMilli()),
		fmt.Sprintf("remind_warning:%d:cert:fp:14:%d:1", id, now.UnixMilli()),
	}
	for _, pendingKey := range keys {
		if _, _, err := st.ClaimNotificationID(t.Context(), pendingKey, string(channel.Type), channel.ID, channel.Name, channel.NotificationTarget(), "payload", "Example", now); err != nil {
			t.Fatalf("ClaimNotificationID(%q): %v", pendingKey, err)
		}
	}
	notification := model.Notification{
		Key: key, Kind: model.EventDown, MonitorID: id, MonitorName: "Example",
		MonitorTarget: "https://example.com", MonitorType: model.MonitorTypeHTTP,
		Status: model.StatusDown, OccurredAt: now,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := st.EnqueueNotificationOutbox(ctx, notification, now); err != nil {
		t.Fatalf("EnqueueNotificationOutbox: %v", err)
	}

	if err := st.DeleteMonitor(ctx, id); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	logs, err := st.RecentNotifications(ctx, 10)
	if err != nil || len(logs) != 0 {
		t.Fatalf("pending notifications after delete = %+v err=%v", logs, err)
	}
	outbox, err := st.DueNotificationOutbox(ctx, now.Add(time.Hour), 10)
	if err != nil || len(outbox) != 0 {
		t.Fatalf("outbox after delete = %+v err=%v", outbox, err)
	}
}
