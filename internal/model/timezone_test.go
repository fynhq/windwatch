package model

import (
	"testing"
	"time"
)

func TestLoadTimezone(t *testing.T) {
	for _, value := range []string{"", "UTC", "Asia/Shanghai"} {
		location, err := LoadTimezone(value)
		if err != nil {
			t.Fatalf("LoadTimezone(%q): %v", value, err)
		}
		if location == nil {
			t.Fatalf("LoadTimezone(%q) returned nil", value)
		}
	}
	if _, err := LoadTimezone("Not/AZone"); err == nil {
		t.Fatal("expected invalid timezone error")
	}
}

func TestValidateTimezoneRejectsFortyFiveMinuteOffset(t *testing.T) {
	if _, err := ValidateTimezone("Asia/Kathmandu"); err == nil {
		t.Fatal("expected 45-minute offset to be rejected")
	}
	if location, err := ValidateTimezone("Asia/Shanghai"); err != nil || location == nil {
		t.Fatalf("ValidateTimezone(Asia/Shanghai) = %v, %v", location, err)
	}
}

func TestTimezoneFromSettings(t *testing.T) {
	if got := TimezoneFromSettings(nil); got != time.UTC {
		t.Fatalf("default location = %s, want UTC", got)
	}
	location := TimezoneFromSettings(map[string]string{SettingTimezone: "Asia/Shanghai"})
	if location.String() != "Asia/Shanghai" {
		t.Fatalf("location = %s, want Asia/Shanghai", location)
	}
	if got := TimezoneFromSettings(map[string]string{SettingTimezone: "bad/zone"}); got != time.UTC {
		t.Fatalf("invalid location = %s, want UTC", got)
	}
}
