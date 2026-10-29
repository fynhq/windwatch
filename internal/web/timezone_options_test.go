package web

import (
	"testing"
	"time"
)

func TestBuildTimezoneOptions(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	options := buildTimezoneOptions("Asia/Shanghai", now)
	if len(options) < 50 {
		t.Fatalf("timezone options = %d, want a broad list", len(options))
	}
	foundUTC := false
	foundShanghai := false
	for _, option := range options {
		switch option.Value {
		case "UTC":
			foundUTC = option.Selected == false
		case "Asia/Shanghai":
			foundShanghai = option.Selected
		case "Asia/Kathmandu", "Australia/Eucla":
			t.Fatalf("unsupported timezone %q is offered", option.Value)
		}
	}
	if !foundUTC || !foundShanghai {
		t.Fatalf("expected UTC and selected Asia/Shanghai: %+v", options)
	}
}

func TestBuildTimezoneOptionsKeepsValidUnlistedCurrent(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	options := buildTimezoneOptions("Australia/Lord_Howe", now)
	if len(options) == 0 || options[0].Value != "Australia/Lord_Howe" || !options[0].Selected {
		t.Fatalf("current timezone was not preserved: %+v", options)
	}
}
