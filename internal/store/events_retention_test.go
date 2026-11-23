package store

import (
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
)

func TestDeleteEventsBeforePrunesRecoveryEventsByAge(t *testing.T) {
	st := newStoreTest(t)
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	monitorID, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Retention", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: time.Second, FailThreshold: 1, RecoverThreshold: 1,
	}, now)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	cutoff := now.Add(-365 * 24 * time.Hour)
	old := cutoff.Add(-time.Hour)
	closedOld := old.Add(30 * time.Minute)
	recent := now.Add(-time.Hour)
	recentDown := recent.Add(-time.Minute)
	closedRecent := now.Add(-30 * time.Minute)

	seed := []struct {
		typ     model.EventType
		started time.Time
		ended   int64
	}{
		{model.EventUp, old, 0},
		{model.EventDown, old, closedOld.UTC().UnixMilli()},
		{model.EventDown, old, 0},
		{model.EventUp, recent, 0},
		{model.EventDown, recentDown, closedRecent.UTC().UnixMilli()},
	}
	for _, row := range seed {
		var ended any
		if row.ended != 0 {
			ended = row.ended
		}
		if _, err := st.db.ExecContext(t.Context(), `
			INSERT INTO events (monitor_id, type, message, started_at_ms, ended_at_ms, meta_json)
			VALUES (?, ?, '', ?, ?, '{}')
		`, monitorID, string(row.typ), row.started.UTC().UnixMilli(), ended); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}

	if err := st.DeleteEventsBefore(t.Context(), cutoff); err != nil {
		t.Fatalf("DeleteEventsBefore: %v", err)
	}

	rows, err := st.db.QueryContext(t.Context(), `
		SELECT type, started_at_ms, ended_at_ms FROM events WHERE monitor_id = ? ORDER BY started_at_ms
	`, monitorID)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	type remaining struct {
		typ     string
		started int64
		ended   int64
	}
	var got []remaining
	for rows.Next() {
		var r remaining
		var ended *int64
		if err := rows.Scan(&r.typ, &r.started, &ended); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		if ended != nil {
			r.ended = *ended
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events: %v", err)
	}
	want := []remaining{
		{string(model.EventDown), old.UTC().UnixMilli(), 0},
		{string(model.EventDown), recentDown.UTC().UnixMilli(), closedRecent.UTC().UnixMilli()},
		{string(model.EventUp), recent.UTC().UnixMilli(), 0},
	}
	if len(got) != len(want) {
		t.Fatalf("remaining events = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining event[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
