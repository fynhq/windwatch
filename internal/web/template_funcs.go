package web

import (
	"html/template"

	"github.com/fynhq/windwatch/internal/model"
)

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"monitorNameMax":           func() int { return model.MaxMonitorNameLength },
		"urlMaxBytes":              func() int { return model.MaxURLBytes },
		"tcpHostMaxBytes":          func() int { return model.MaxTCPHostBytes },
		"dockerContainerMax":       func() int { return model.MaxDockerContainerLength },
		"portMin":                  func() int { return model.MinPort },
		"portMax":                  func() int { return model.MaxPort },
		"intervalMin":              func() int { return model.MinIntervalSeconds },
		"intervalMax":              func() int { return model.MaxIntervalSeconds },
		"timeoutMin":               func() int { return model.MinTimeoutSeconds },
		"timeoutMax":               func() int { return model.MaxTimeoutSeconds },
		"thresholdMin":             func() int { return model.MinThreshold },
		"thresholdMax":             func() int { return model.MaxThreshold },
		"certWarnMin":              func() int { return model.MinCertWarnDays },
		"certWarnMax":              func() int { return model.MaxCertWarnDays },
		"keywordMaxRunes":          func() int { return model.MaxHTTPKeywordRunes },
		"expectedStatusMaxRunes":   func() int { return model.MaxExpectedStatusLength },
		"expectedStatusDefault":    func() string { return model.DefaultExpectedStatus },
		"reminderCountMin":         func() int { return model.MinNotificationReminders },
		"reminderCountMax":         func() int { return model.MaxNotificationReminders },
		"reminderIntervalMin":      func() int { return model.MinReminderIntervalMinutes },
		"reminderIntervalMax":      func() int { return model.MaxReminderIntervalMinutes },
		"publicTitleMaxRunes":      func() int { return model.MaxPublicStatusTitleLength },
		"publicDescriptionMax":     func() int { return model.MaxPublicStatusDescriptionLength },
		"usernameMinRunes":         func() int { return model.MinUsernameLength },
		"usernameMaxRunes":         func() int { return model.MaxUsernameLength },
		"passwordMinRunes":         func() int { return model.MinPasswordLength },
		"passwordMaxRunes":         func() int { return model.MaxPasswordLength },
		"notificationNameMaxRunes": func() int { return model.MaxNotificationChannelNameLength },
		"proxyNameMaxRunes":        func() int { return model.MaxProxyNameLength },
	}
}
