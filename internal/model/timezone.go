package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	SettingTimezone = "timezone"
	DefaultTimezone = "UTC"
)

func LoadTimezone(value string) (*time.Location, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.UTC, nil
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", value, err)
	}
	return location, nil
}

func ValidateTimezone(value string) (*time.Location, error) {
	location, err := LoadTimezone(value)
	if err != nil {
		return nil, err
	}
	_, offset := time.Now().In(location).Zone()
	if offset%1800 != 0 {
		return nil, fmt.Errorf("timezone offset must be a multiple of 30 minutes")
	}
	return location, nil
}

func TimezoneFromSettings(values map[string]string) *time.Location {
	location, err := LoadTimezone(values[SettingTimezone])
	if err != nil {
		return time.UTC
	}
	return location
}
