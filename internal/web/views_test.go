package web

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
)

func TestFormatRelativeTime(t *testing.T) {
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	translator := catalogs.Translator(i18n.LangEN)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		value time.Time
		want  string
	}{
		{time.Time{}, "Never"},
		{now.Add(-2 * time.Second), "just now"},
		{now.Add(-12 * time.Second), "12 seconds ago"},
		{now.Add(-3 * time.Minute), "3 min ago"},
		{now.Add(-2 * time.Hour), "2 hr ago"},
		{now.Add(-25 * time.Hour), "1 day ago"},
		{now.Add(-3 * 24 * time.Hour), "3 days ago"},
		{now.Add(time.Minute), "just now"},
	}
	for _, item := range cases {
		if got := formatRelativeTime(translator, now, item.value); got != item.want {
			t.Fatalf("formatRelativeTime(%s) = %q, want %q", item.value, got, item.want)
		}
	}
}

func TestDashboardOverall(t *testing.T) {
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	translator := catalogs.Translator(i18n.LangEN)
	cases := []struct {
		name  string
		total int
		up    int
		warn  int
		down  int
		pend  int
		pause int
		class string
		label string
	}{
		{"all up", 2, 2, 0, 0, 0, 0, "status-up", "All systems operational"},
		{"down wins", 2, 1, 1, 1, 0, 0, "status-down", "Some monitors are down"},
		{"warning", 2, 1, 1, 0, 0, 0, "status-warning", "Some warnings"},
		{"pending", 2, 1, 0, 0, 1, 0, "status-pending", "Waiting for first check"},
		{"all paused", 2, 0, 0, 0, 0, 2, "status-paused", "All monitors paused"},
	}
	for _, item := range cases {
		class, label := dashboardOverall(translator, item.total, item.up, item.warn, item.down, item.pend, item.pause)
		if class != item.class || label != item.label {
			t.Fatalf("%s: got (%s, %q), want (%s, %q)", item.name, class, label, item.class, item.label)
		}
	}
}

func TestPublicOverallIgnoresPaused(t *testing.T) {
	cases := []struct {
		name  string
		total int
		up    int
		warn  int
		down  int
		pend  int
		pause int
		class string
		key   string
	}{
		{"paused only", 2, 0, 0, 0, 0, 2, "status-paused", "public.allPaused"},
		{"paused with up", 2, 1, 0, 0, 0, 1, "status-up", "public.allOperational"},
		{"down wins", 2, 1, 0, 1, 0, 0, "status-down", "public.someDown"},
		{"warning", 2, 1, 1, 0, 0, 0, "status-warning", "public.someAttention"},
		{"pending", 2, 1, 0, 0, 1, 0, "status-warning", "public.someAttention"},
		{"empty", 0, 0, 0, 0, 0, 0, "status-pending", "public.noMonitors"},
	}
	for _, item := range cases {
		key, class := publicOverall(item.total, item.up, item.warn, item.down, item.pend, item.pause)
		if key != item.key || class != item.class {
			t.Fatalf("%s: got (%s, %s), want (%s, %s)", item.name, key, class, item.key, item.class)
		}
	}
}

func TestBuildPublicHistoryBars(t *testing.T) {
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	translator := catalogs.Translator(i18n.LangEN)
	location := time.UTC
	today := time.Date(2026, 10, 29, 0, 0, 0, 0, location)
	buckets := []model.HourBucket{
		{Start: today.AddDate(0, 0, -2), Total: 4, Up: 4},
		{Start: today.AddDate(0, 0, -1), Total: 4, Up: 3, Down: 1},
	}
	bars := buildPublicHistoryBars(translator, buckets, today, 3, location)
	if len(bars) != 3 {
		t.Fatalf("history bars = %d, want 3", len(bars))
	}
	if bars[0].Class != "bar-empty" || bars[1].Class != "bar-up" || bars[2].Class != "bar-down" {
		t.Fatalf("history classes = %q, %q, %q", bars[0].Class, bars[1].Class, bars[2].Class)
	}
	if bars[2].Title == "" {
		t.Fatal("history bar title is empty")
	}
}
