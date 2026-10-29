package i18n

import (
	"strings"
	"time"
)

// FormatDuration renders a duration with localized unit labels.
func FormatDuration(translator *Translator, value time.Duration) string {
	if value <= 0 {
		return "—"
	}
	value = value.Round(time.Second)
	if value < time.Second {
		return translator.T("duration.lessThanSecond")
	}

	days := value / (24 * time.Hour)
	value -= days * 24 * time.Hour
	hours := value / time.Hour
	value -= hours * time.Hour
	minutes := value / time.Minute
	value -= minutes * time.Minute
	seconds := value / time.Second

	parts := make([]string, 0, 4)
	if days > 0 {
		parts = append(parts, translator.Tf("duration.days", days))
	}
	if hours > 0 {
		parts = append(parts, translator.Tf("duration.hours", hours))
	}
	if minutes > 0 {
		parts = append(parts, translator.Tf("duration.minutes", minutes))
	}
	if seconds > 0 {
		parts = append(parts, translator.Tf("duration.seconds", seconds))
	}
	return strings.Join(parts, " ")
}
