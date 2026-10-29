package model

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type NotificationChannelType string

const (
	NotificationChannelTelegram NotificationChannelType = "telegram"
	NotificationChannelWebhook  NotificationChannelType = "webhook"
)

const (
	NotificationLanguageZH = "zh-CN"
	NotificationLanguageEN = "en"
)

type NotificationChannel struct {
	ID              int64
	Name            string
	Type            NotificationChannelType
	Language        string
	Enabled         bool
	TelegramChatIDs string
	WebhookURL      string
	SecretEnc       string
	ProxyID         *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

var (
	ErrChannelName       = errors.New("invalid notification channel name")
	ErrChannelType       = errors.New("invalid notification channel type")
	ErrChannelLanguage   = errors.New("invalid notification channel language")
	ErrChannelChatIDs    = errors.New("invalid telegram chat ids")
	ErrChannelWebhookURL = errors.New("invalid webhook URL")
)

func (c NotificationChannel) Validate() error {
	name := strings.TrimSpace(c.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return ErrChannelName
	}
	if language := strings.TrimSpace(c.Language); language != "" && !validNotificationLanguage(language) {
		return ErrChannelLanguage
	}
	switch c.Type {
	case NotificationChannelTelegram:
		chatIDs := splitChannelChatIDs(c.TelegramChatIDs)
		if len(chatIDs) == 0 {
			return ErrChannelChatIDs
		}
		for _, chatID := range chatIDs {
			if !validChatID(chatID) {
				return ErrChannelChatIDs
			}
		}
	case NotificationChannelWebhook:
		if err := validateChannelWebhookURL(c.WebhookURL); err != nil {
			return err
		}
	default:
		return ErrChannelType
	}
	return nil
}

func validateChannelWebhookURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ErrChannelWebhookURL
	}
	return nil
}

func splitChannelChatIDs(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// NotificationTarget returns a short, non-secret summary for lists and logs.
func (c NotificationChannel) NotificationTarget() string {
	switch c.Type {
	case NotificationChannelTelegram:
		return strings.TrimSpace(c.TelegramChatIDs)
	case NotificationChannelWebhook:
		parsed, err := url.Parse(strings.TrimSpace(c.WebhookURL))
		if err == nil && parsed.Host != "" {
			return parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
		}
		return strings.TrimSpace(c.WebhookURL)
	default:
		return ""
	}
}

func (c NotificationChannel) ProxyIDString() string {
	if c.ProxyID == nil {
		return ""
	}
	return strconv.FormatInt(*c.ProxyID, 10)
}

func (c NotificationChannel) ResolvedLanguage() string {
	if strings.TrimSpace(c.Language) == NotificationLanguageEN {
		return NotificationLanguageEN
	}
	return NotificationLanguageZH
}

func validNotificationLanguage(value string) bool {
	return value == NotificationLanguageZH || value == NotificationLanguageEN
}

func (c NotificationChannel) ChatIDs() []string {
	return splitChannelChatIDs(c.TelegramChatIDs)
}

func validChatID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	if strings.HasPrefix(value, "@") {
		return len(value) > 1 && !strings.ContainsAny(value[1:], " \t\r\n")
	}
	_, err := strconv.ParseInt(value, 10, 64)
	return err == nil
}
