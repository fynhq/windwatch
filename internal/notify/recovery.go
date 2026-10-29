package notify

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

const (
	pendingMaxAge         = 24 * time.Hour
	pendingRecoveryCap    = 200
	pendingRecoveryBudget = 2 * time.Minute
)

func (s *Service) recoverPending(ctx context.Context) {
	recoverCtx, cancel := context.WithTimeout(ctx, pendingRecoveryBudget)
	defer cancel()
	entries, err := s.store.PendingNotifications(recoverCtx, pendingRecoveryCap)
	if err != nil {
		s.log.Error("load pending notifications", "error", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	recovered := 0
	for _, entry := range entries {
		if recoverCtx.Err() != nil {
			s.log.Warn("pending notification recovery stopped", "recovered", recovered, "remaining", len(entries)-recovered)
			return
		}
		s.log.Info("resending pending notification", "channel", entry.Channel, "channel_id", entry.ChannelID, "target", entry.Target, "dedupe_key", entry.DedupeKey, "created_at", entry.CreatedAt.UTC().Format(time.RFC3339))
		if err := s.recoverEntry(recoverCtx, entry); err != nil {
			if recoverCtx.Err() != nil {
				s.log.Warn("pending notification recovery stopped", "recovered", recovered, "remaining", len(entries)-recovered)
				return
			}
			s.log.Warn("recovery send failed", "channel", entry.Channel, "target", entry.Target, "error", err)
			if completeErr := s.store.CompleteNotification(recoverCtx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, "failed", truncateError(err.Error()), time.Now().UTC()); completeErr != nil {
				s.log.Error("record recovered notification", "error", completeErr)
			}
			continue
		}
		recovered++
	}
	if recovered > 0 {
		s.log.Info("recovered pending notifications", "count", recovered)
	}
}

func (s *Service) recoverEntry(ctx context.Context, entry model.NotificationLog) error {
	if strings.TrimSpace(entry.Payload) == "" {
		return errors.New("pending notification has no payload")
	}
	if time.Since(entry.CreatedAt) > pendingMaxAge {
		return errors.New("pending notification expired")
	}
	if entry.ChannelID <= 0 {
		return errors.New("notification channel no longer exists")
	}
	channel, err := s.store.NotificationChannelByID(ctx, entry.ChannelID)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("notification channel no longer exists")
	}
	if err != nil {
		return err
	}
	if !channel.Enabled {
		return errors.New("notification channel is disabled")
	}
	switch channel.Type {
	case model.NotificationChannelTelegram:
		if !containsChatID(channel, entry.Target) {
			return errors.New("telegram chat id is no longer configured")
		}
		token, err := s.channelSecret(channel)
		if err != nil {
			return err
		}
		client, cleanup, err := s.httpClient(ctx, channel.ProxyID)
		if err != nil {
			return err
		}
		defer cleanup()
		if err := s.retry(ctx, func() error {
			return s.postTelegram(ctx, client, token, entry.Target, entry.Payload)
		}); err != nil {
			return err
		}
	case model.NotificationChannelWebhook:
		if err := s.sendWebhookBody(ctx, channel, []byte(entry.Payload)); err != nil {
			return err
		}
	default:
		return errors.New("unknown notification channel type")
	}
	return s.store.CompleteNotification(ctx, entry.DedupeKey, entry.Channel, entry.ChannelID, entry.Target, "sent", "", time.Now().UTC())
}

func containsChatID(channel model.NotificationChannel, target string) bool {
	for _, chatID := range channel.ChatIDs() {
		if chatID == strings.TrimSpace(target) {
			return true
		}
	}
	return false
}
