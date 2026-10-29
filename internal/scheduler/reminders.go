package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

const (
	reminderKindDown    = "down"
	reminderKindWarning = "warning"
)

func (s *Scheduler) processReminders(
	ctx context.Context,
	monitor model.Monitor,
	result model.CheckResult,
	warning auxWarning,
	warningActive bool,
	nextState model.MonitorState,
	previousStatus model.MonitorStatus,
	checkedAt time.Time,
) {
	if s.notifier == nil || !monitor.Enabled || !monitorNotificationsEnabled(monitor) {
		if previousStatus != nextState.Status {
			s.clearReminderStates(ctx, monitor.ID)
		}
		return
	}

	currentKind := ""
	switch nextState.Status {
	case model.StatusDown:
		currentKind = reminderKindDown
	case model.StatusWarning:
		currentKind = reminderKindWarning
	}
	if previousStatus != nextState.Status {
		if currentKind != reminderKindDown {
			s.deleteReminderState(ctx, monitor.ID, reminderKindDown)
		}
		if currentKind != reminderKindWarning {
			s.deleteReminderState(ctx, monitor.ID, reminderKindWarning)
		}
	}
	if currentKind == "" {
		return
	}
	if currentKind == reminderKindWarning && !warningActive {
		return
	}

	policy := monitor.Reminders.Normalized()
	count, interval := policy.FailureReminderCount, policy.FailureReminderInterval
	if currentKind == reminderKindWarning {
		count, interval = policy.WarningReminderCount, policy.WarningReminderInterval
	}
	if count <= 0 {
		if previousStatus != nextState.Status {
			s.deleteReminderState(ctx, monitor.ID, currentKind)
		}
		return
	}

	contextKey := ""
	if currentKind == reminderKindWarning {
		contextKey = warningReminderContext(monitor, warning, result)
	}
	state, found, err := s.store.ReminderState(ctx, monitor.ID, currentKind)
	if err != nil {
		s.log.Warn("load reminder state", "monitor_id", monitor.ID, "error", err)
		return
	}
	if !found ||
		state.StatusSince.UnixMilli() != nextState.StatusSince.UnixMilli() ||
		state.ContextKey != contextKey {
		state = model.NotificationReminder{
			MonitorID:   monitor.ID,
			Kind:        currentKind,
			StatusSince: nextState.StatusSince,
			ContextKey:  contextKey,
			NextAt:      nextState.StatusSince.Add(interval),
		}
		if err := s.store.SaveReminderState(ctx, state, checkedAt); err != nil {
			s.log.Warn("save reminder state", "monitor_id", monitor.ID, "error", err)
			return
		}
	}
	if state.SentCount >= count || checkedAt.UnixMilli() < state.NextAt.UnixMilli() {
		return
	}
	if !s.notificationsAvailable(ctx) {
		return
	}

	sequence := state.SentCount + 1
	downtimeStart := nextState.StatusSince
	if currentKind == reminderKindDown {
		downtimeStart = s.outageStart(ctx, monitor.ID, nextState.StatusSince, checkedAt)
	}
	notification := s.buildReminderNotification(monitor, result, warning, nextState, checkedAt, currentKind, contextKey, sequence, count, downtimeStart)
	if !s.notifier.Enqueue(notification) {
		return
	}
	s.log.Info("reminder notification enqueued", "monitor_id", monitor.ID, "kind", currentKind, "sequence", sequence, "total", count, "next_at", checkedAt.Add(interval).UTC().Format(time.RFC3339))
	state.SentCount = sequence
	state.NextAt = checkedAt.Add(interval)
	if err := s.store.SaveReminderState(ctx, state, checkedAt); err != nil {
		s.log.Warn("advance reminder state", "monitor_id", monitor.ID, "error", err)
	}
}

func (s *Scheduler) buildReminderNotification(
	monitor model.Monitor,
	result model.CheckResult,
	warning auxWarning,
	nextState model.MonitorState,
	checkedAt time.Time,
	kind, contextKey string,
	sequence, total int,
	downtimeStart time.Time,
) model.Notification {
	if kind == reminderKindDown {
		downtime := checkedAt.Sub(downtimeStart)
		if downtime < 0 {
			downtime = 0
		}
		return model.Notification{
			Key:            fmt.Sprintf("remind_down:%d:%d:%d", monitor.ID, nextState.StatusSince.UnixMilli(), sequence),
			Kind:           model.EventDown,
			MonitorID:      monitor.ID,
			MonitorName:    monitor.Name,
			MonitorTarget:  monitor.Target(),
			MonitorType:    monitor.Type,
			Status:         model.StatusDown,
			Message:        nextState.LastError,
			OccurredAt:     checkedAt,
			Downtime:       downtime,
			DowntimeStart:  downtimeStart,
			DowntimeCycles: outageCycles(downtime, monitor.Interval, monitor.Type),
			Reminder:       sequence,
			ReminderTotal:  total,
		}
	}
	notification := s.warningNotification(monitor, result, warning, nextState, checkedAt)
	notification.Key = fmt.Sprintf("remind_warning:%d:%s:%d:%d", monitor.ID, contextKey, nextState.StatusSince.UnixMilli(), sequence)
	notification.Reminder = sequence
	notification.ReminderTotal = total
	return notification
}

func warningReminderContext(monitor model.Monitor, warning auxWarning, result model.CheckResult) string {
	if monitor.HTTP == nil {
		return "warning"
	}
	if warning.cert && result.Certificate != nil {
		return fmt.Sprintf("cert:%s:%d", result.Certificate.Fingerprint, monitor.HTTP.CertWarnDays)
	}
	if warning.domainInfo != nil {
		return fmt.Sprintf("domain:%s:%d:%d", warning.domainName, warning.domainInfo.ExpiresAt.UnixMilli(), monitor.HTTP.DomainWarnDays)
	}
	return "warning"
}

func (s *Scheduler) clearReminderStates(ctx context.Context, monitorID int64) {
	if err := s.store.DeleteReminderStates(ctx, monitorID); err != nil {
		s.log.Warn("clear reminder states", "monitor_id", monitorID, "error", err)
	}
}

func (s *Scheduler) deleteReminderState(ctx context.Context, monitorID int64, kind string) {
	if err := s.store.DeleteReminderState(ctx, monitorID, kind); err != nil {
		s.log.Warn("delete reminder state", "monitor_id", monitorID, "kind", kind, "error", err)
	}
}
