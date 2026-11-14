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
		contextKey = warningReminderContext(monitor, result)
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
	notification := s.buildReminderNotification(monitor, result, nextState, checkedAt, currentKind, contextKey, sequence, count, downtimeStart)
	state.SentCount = sequence
	state.NextAt = checkedAt.Add(interval)
	if err := s.store.SaveReminderStateWithNotification(ctx, state, checkedAt, &notification); err != nil {
		s.log.Error("persist reminder notification", "monitor_id", monitor.ID, "kind", currentKind, "error", err)
		return
	}
	s.wakeNotifications([]model.Notification{notification})
	s.log.Info("reminder notification enqueued", "monitor_id", monitor.ID, "kind", currentKind, "sequence", sequence, "total", count, "next_at", checkedAt.Add(interval).UTC().Format(time.RFC3339))
}

func (s *Scheduler) buildReminderNotification(
	monitor model.Monitor,
	result model.CheckResult,
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
			Key:            model.DownReminderKey(monitor.ID, nextState.StatusSince, sequence),
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
	notification := s.warningNotification(monitor, result, nextState, checkedAt)
	notification.Key = model.WarningReminderKey(monitor.ID, contextKey, nextState.StatusSince, sequence)
	notification.Reminder = sequence
	notification.ReminderTotal = total
	return notification
}

func warningReminderContext(monitor model.Monitor, result model.CheckResult) string {
	if monitor.HTTP == nil || result.Certificate == nil {
		return "warning"
	}
	return fmt.Sprintf("cert:%s:%d", result.Certificate.Fingerprint, monitor.HTTP.CertWarnDays)
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
