package scheduler

import (
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

type transitionResult struct {
	state          model.MonitorState
	event          *model.Event
	checkStatus    string
	warningChanged bool
	closeDown      bool
	warningContext *string
}

func advance(monitor model.Monitor, current model.MonitorState, result model.CheckResult, at time.Time, warningActive bool, warningKey string) transitionResult {
	previousStatus := current.Status
	previousSince := current.StatusSince
	previousWarningKey := current.WarningContext

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

	var (
		event     *model.Event
		closeDown bool
	)
	if result.Success {
		next.ConsecutiveFailures = 0
		next.ConsecutiveSuccesses++
		next.FailureStartedAt = time.Time{}

		switch {
		case next.Status == model.StatusDown:
			if next.ConsecutiveSuccesses >= recoverThreshold {
				next.Status = model.StatusUp
				next.StatusSince = at
				next.LastError = ""
				event = &model.Event{
					MonitorID: monitor.ID,
					Type:      model.EventUp,
					StartedAt: at,
					MetaJSON:  "{}",
				}
				closeDown = true
			}
		case next.Status == model.StatusPending || next.Status == model.StatusWarning:
			next.Status = model.StatusUp
			next.StatusSince = at
			next.LastError = ""
		default:
			next.LastError = ""
		}
	} else {
		next.ConsecutiveSuccesses = 0
		next.ConsecutiveFailures++
		next.LastError = result.ErrorMessage
		if next.FailureStartedAt.IsZero() {
			next.FailureStartedAt = at
		}
		if next.Status != model.StatusDown && next.ConsecutiveFailures >= failThreshold {
			next.Status = model.StatusDown
			next.StatusSince = at
			event = &model.Event{
				MonitorID: monitor.ID,
				Type:      model.EventDown,
				StartedAt: at,
				MetaJSON:  "{}",
			}
		}
	}

	if next.Status != model.StatusDown && result.Success && warningActive && next.Status != model.StatusWarning {
		if previousStatus == model.StatusWarning && previousWarningKey == warningKey {
			next.StatusSince = previousSince
		} else {
			next.StatusSince = at
		}
		next.Status = model.StatusWarning
	}

	checkStatus := model.CheckDown
	if result.Success {
		checkStatus = model.CheckUp
		if warningActive && next.Status != model.StatusDown {
			checkStatus = model.CheckWarning
		}
	}

	warningChanged := warningActive && next.Status == model.StatusWarning &&
		(previousStatus != model.StatusWarning || previousWarningKey != warningKey)

	var warningContext *string
	if next.Status == model.StatusWarning && warningActive {
		next.WarningContext = warningKey
		value := warningKey
		warningContext = &value
	} else if next.Status != model.StatusWarning {
		next.WarningContext = ""
		if previousWarningKey != "" {
			value := ""
			warningContext = &value
		}
	}

	return transitionResult{
		state:          next,
		event:          event,
		checkStatus:    checkStatus,
		warningChanged: warningChanged,
		closeDown:      closeDown,
		warningContext: warningContext,
	}
}
