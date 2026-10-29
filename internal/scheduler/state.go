package scheduler

import (
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func advance(monitor model.Monitor, current model.MonitorState, result model.CheckResult, at time.Time) (model.MonitorState, *model.Event, bool) {
	next := current
	if next.Status == "" {
		next.Status = model.StatusPending
	}
	if next.StatusSince.IsZero() {
		next.StatusSince = at
	}
	next.LastCheck = at
	next.UpdatedAt = at

	failThreshold := monitor.FailThreshold
	if failThreshold < 1 {
		failThreshold = 1
	}
	recoverThreshold := monitor.RecoverThreshold
	if recoverThreshold < 1 {
		recoverThreshold = 1
	}

	if result.Success {
		next.ConsecutiveFailures = 0
		next.ConsecutiveSuccesses++

		switch {
		case next.Status == model.StatusDown:
			if next.ConsecutiveSuccesses >= recoverThreshold {
				next.Status = model.StatusUp
				next.StatusSince = at
				next.LastError = ""
				return next, &model.Event{
					MonitorID: monitor.ID,
					Type:      model.EventUp,
					StartedAt: at,
					MetaJSON:  "{}",
				}, true
			}
		case next.Status == model.StatusPending || next.Status == model.StatusWarning:
			next.Status = model.StatusUp
			next.StatusSince = at
			next.LastError = ""
		default:
			next.LastError = ""
		}
		return next, nil, false
	}

	next.ConsecutiveSuccesses = 0
	next.ConsecutiveFailures++
	next.LastError = result.ErrorMessage
	if next.Status != model.StatusDown && next.ConsecutiveFailures >= failThreshold {
		next.Status = model.StatusDown
		next.StatusSince = at
		return next, &model.Event{
			MonitorID: monitor.ID,
			Type:      model.EventDown,
			StartedAt: at,
			MetaJSON:  "{}",
		}, false
	}
	return next, nil, false
}
