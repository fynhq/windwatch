package web

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
)

func TestBuildYearHeatmapGrid(t *testing.T) {
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	translator := catalogs.Translator(i18n.LangEN)
	location := time.UTC
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, location)
	today := time.Date(2026, 11, 11, 0, 0, 0, 0, location)

	view := buildYearHeatmap(translator, now, []model.HourBucket{
		{Start: today.AddDate(0, 0, -1), Total: 10, Up: 10},
		{Start: today, Total: 10, Up: 9, Warning: 1},
	}, location)

	if !view.HasData || view.Summary == "" || view.AriaLabel == "" {
		t.Fatalf("year heatmap summary missing: %+v", view)
	}
	if len(view.Rows) != 7 {
		t.Fatalf("rows = %d, want 7", len(view.Rows))
	}
	for index, row := range view.Rows {
		if len(row.Cells) != 53 {
			t.Fatalf("row %d cells = %d, want 53", index, len(row.Cells))
		}
	}
	span := 0
	for _, month := range view.Months {
		span += month.Span
	}
	if span != 53 {
		t.Fatalf("month spans = %d, want 53", span)
	}

	todayCell := view.Rows[2].Cells[52]
	if todayCell.Class != "heat-warn" {
		t.Fatalf("today cell class = %q, want heat-warn", todayCell.Class)
	}
	if got := view.Rows[4].Cells[52].Class; got != "heat-future" {
		t.Fatalf("future cell class = %q, want heat-future", got)
	}
	if got := view.Rows[2].Cells[51].Class; got != "heat-empty" {
		t.Fatalf("missing day class = %q, want heat-empty", got)
	}
	if view.RangeLabel == "" {
		t.Fatal("range label is empty")
	}
}

func TestBuildYearHeatmapWithoutData(t *testing.T) {
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	translator := catalogs.Translator(i18n.LangEN)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	view := buildYearHeatmap(translator, now, nil, time.UTC)
	if view.HasData {
		t.Fatal("empty buckets should not report heatmap data")
	}
	if view.Summary != translator.T("monitor.noLongRangeData") {
		t.Fatalf("summary = %q", view.Summary)
	}
	if len(view.Rows) != 7 || len(view.Rows[0].Cells) != 53 {
		t.Fatalf("heatmap grid shape = %d rows", len(view.Rows))
	}
}

func TestShortMonthLabelRespectsLocale(t *testing.T) {
	catalogs, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	value := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	if got := shortMonthLabel(catalogs.Translator(i18n.LangZH), value); got != "11月" {
		t.Fatalf("zh short month = %q, want 11月", got)
	}
	if got := shortMonthLabel(catalogs.Translator(i18n.LangEN), value); got != "Nov" {
		t.Fatalf("en short month = %q, want Nov", got)
	}
}
