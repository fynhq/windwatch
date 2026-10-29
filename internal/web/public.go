package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
)

type publicStatusFormValues struct {
	Enabled     bool
	Title       string
	Description string
	MonitorIDs  []int64
}

type publicMonitorOption struct {
	ID        int64
	Name      string
	TypeLabel string
	Selected  bool
}

type publicMonitorRow struct {
	Name        string
	TypeLabel   string
	StatusClass string
	StatusLabel string
	Uptime24    string
	Uptime7d    string
	Uptime30d   string
	Uptime90d   string
	History     []statusBar
}

type publicStatusPage struct {
	pageData
	Description  string
	Overall      string
	OverallClass string
	Updated      string
	Monitors     []publicMonitorRow
	HasMonitors  bool
	Total        int
	UpCount      int
	WarningCount int
	DownCount    int
	PendingCount int
	PausedCount  int
}

func publicStatusFromSettings(values map[string]string) publicStatusFormValues {
	value := publicStatusFormValues{
		Enabled:     values[model.SettingPublicStatusEnabled] == "1",
		Title:       strings.TrimSpace(values[model.SettingPublicStatusTitle]),
		Description: strings.TrimSpace(values[model.SettingPublicStatusDescription]),
	}
	var ids []int64
	if raw := values[model.SettingPublicStatusMonitorIDs]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			ids = nil
		}
	}
	value.MonitorIDs = uniquePositiveIDs(ids)
	return value
}

func publicOverall(total, up, warning, down, pending, paused int) (string, string) {
	switch {
	case total == 0:
		return "public.noMonitors", "status-pending"
	case down > 0:
		return "public.someDown", "status-down"
	case warning > 0 || pending > 0:
		return "public.someAttention", "status-warning"
	case up == 0 && paused == total:
		return "public.allPaused", "status-paused"
	default:
		return "public.allOperational", "status-up"
	}
}

func buildPublicHistoryBars(translator *i18n.Translator, buckets []model.HourBucket, end time.Time, days int, location *time.Location) []statusBar {
	if days <= 0 {
		return nil
	}
	if location == nil {
		location = time.UTC
	}
	byStart := make(map[int64]model.HourBucket, len(buckets))
	for _, bucket := range buckets {
		byStart[bucket.Start.UnixMilli()] = bucket
	}
	start := end.AddDate(0, 0, -days)
	bars := make([]statusBar, 0, days)
	for index := 0; index < days; index++ {
		day := start.AddDate(0, 0, index)
		label := formatChartDate(translator, day, location)
		bucket, ok := byStart[day.UnixMilli()]
		if !ok || bucket.Total == 0 {
			bars = append(bars, statusBar{Class: "bar-empty", Title: label + " · " + translator.T("common.noData")})
			continue
		}
		good := bucket.Up + bucket.Warning
		ratio := float64(good) / float64(bucket.Total)
		class := "bar-down"
		switch {
		case ratio >= 0.999:
			class = "bar-up"
		case ratio >= 0.95:
			class = "bar-mixed"
		}
		bars = append(bars, statusBar{
			Class: class,
			Title: label + " · " + formatPercent(good, bucket.Total),
		})
	}
	return bars
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	values, err := s.store.Settings(r.Context())
	if err != nil {
		s.log.Error("load public status settings", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	status := publicStatusFromSettings(values)
	if !status.Enabled {
		s.renderError(w, r, http.StatusNotFound, "error.notFound")
		return
	}

	now := s.now().UTC()
	data := s.newPage(r, "public.titleDefault")
	data.PublicView = true
	data.Path = "/status"
	data.AutoRefresh = true
	title := status.Title
	if title == "" {
		title = data.I18n.T("public.titleDefault")
	}
	data.Title = title
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	publicMonitors, err := s.store.MonitorsByIDs(r.Context(), status.MonitorIDs)
	if err != nil {
		s.log.Error("load public monitors", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	monitorIDs := make([]int64, 0, len(publicMonitors))
	for _, monitor := range publicMonitors {
		monitorIDs = append(monitorIDs, monitor.ID)
	}
	location := data.Location
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	history, err := s.store.DailyBucketsForMonitors(r.Context(), monitorIDs, today.AddDate(0, 0, -90), today, location)
	if err != nil {
		s.log.Error("load public daily history", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	stats24ByMonitor, err := s.store.UptimeStatsForMonitors(r.Context(), monitorIDs, now.Add(-24*time.Hour))
	if err != nil {
		s.log.Error("load public 24h stats", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	stats7dByMonitor, err := s.store.UptimeStatsForMonitors(r.Context(), monitorIDs, now.Add(-7*24*time.Hour))
	if err != nil {
		s.log.Error("load public 7d stats", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	stats30OlderByMonitor, err := s.store.RollupStatsForMonitors(r.Context(), monitorIDs, now.Add(-30*24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		s.log.Error("load public 30d stats", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	stats90OlderByMonitor, err := s.store.RollupStatsForMonitors(r.Context(), monitorIDs, now.Add(-90*24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		s.log.Error("load public 90d stats", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}

	rows := make([]publicMonitorRow, 0, len(publicMonitors))
	var upCount, warningCount, downCount, pendingCount, pausedCount int
	for _, monitor := range publicMonitors {
		stats24 := stats24ByMonitor[monitor.ID]
		stats7d := stats7dByMonitor[monitor.ID]
		stats30 := combineUptimeStats(stats7d, stats30OlderByMonitor[monitor.ID])
		stats90 := combineUptimeStats(stats7d, stats90OlderByMonitor[monitor.ID])

		label := statusLabel(data.I18n, monitor.Enabled, monitor.State.Status)
		switch {
		case !monitor.Enabled:
			pausedCount++
		case monitor.State.Status == model.StatusDown:
			downCount++
		case monitor.State.Status == model.StatusWarning:
			warningCount++
		case monitor.State.Status == model.StatusUp:
			upCount++
		default:
			pendingCount++
		}
		rows = append(rows, publicMonitorRow{
			Name:        monitor.Name,
			TypeLabel:   s.typeLabel(data.I18n, monitor.Type),
			StatusClass: statusClass(monitor.Enabled, monitor.State.Status),
			StatusLabel: label,
			Uptime24:    formatPercent(stats24.Up+stats24.Warning, stats24.Total),
			Uptime7d:    formatPercent(stats7d.Up+stats7d.Warning, stats7d.Total),
			Uptime30d:   formatPercent(stats30.Up+stats30.Warning, stats30.Total),
			Uptime90d:   formatPercent(stats90.Up+stats90.Warning, stats90.Total),
			History:     buildPublicHistoryBars(data.I18n, history[monitor.ID], today, 90, location),
		})
	}

	overall, overallClass := publicOverall(len(rows), upCount, warningCount, downCount, pendingCount, pausedCount)

	page := publicStatusPage{
		pageData:     data,
		Description:  status.Description,
		Overall:      data.I18n.T(overall),
		OverallClass: overallClass,
		Updated:      formatTime(data.I18n, now, data.Location),
		Monitors:     rows,
		HasMonitors:  len(rows) > 0,
		Total:        len(rows),
		UpCount:      upCount,
		WarningCount: warningCount,
		DownCount:    downCount,
		PendingCount: pendingCount,
		PausedCount:  pausedCount,
	}
	if r.Header.Get(partialRefreshHeader) == "status" {
		s.renderFragment(w, "status", http.StatusOK, page)
		return
	}
	s.render(w, "status", http.StatusOK, page)
}

func (s *Server) handlePublicStatusSave(w http.ResponseWriter, r *http.Request, auth model.AuthInfo) {
	if !s.authorizedPost(w, r, auth) {
		return
	}
	values := publicStatusFormValues{
		Enabled:     r.PostFormValue("public_status_enabled") == "1",
		Title:       strings.TrimSpace(r.PostFormValue("public_status_title")),
		Description: strings.TrimSpace(r.PostFormValue("public_status_description")),
	}
	if !values.Enabled {
		settings, err := s.store.Settings(r.Context())
		if err != nil {
			s.log.Error("load public status settings", "error", err)
			s.renderError(w, r, http.StatusInternalServerError, "error.internal")
			return
		}
		existing := publicStatusFromSettings(settings)
		values.Title = existing.Title
		values.Description = existing.Description
		values.MonitorIDs = existing.MonitorIDs
	}
	if utf8.RuneCountInString(values.Title) > model.MaxPublicStatusTitleLength {
		s.renderSettingsPublicError(w, r, auth, http.StatusBadRequest, values, "error.publicTitle")
		return
	}
	if utf8.RuneCountInString(values.Description) > model.MaxPublicStatusDescriptionLength {
		s.renderSettingsPublicError(w, r, auth, http.StatusBadRequest, values, "error.publicDescription")
		return
	}

	monitors, err := s.store.ListMonitors(r.Context())
	if err != nil {
		s.log.Error("load monitors for public status save", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	existing := make(map[int64]struct{}, len(monitors))
	for _, monitor := range monitors {
		existing[monitor.ID] = struct{}{}
	}
	if values.Enabled {
		ids := make([]int64, 0, len(monitors))
		for _, raw := range r.PostForm["public_monitors"] {
			id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil || id <= 0 {
				continue
			}
			if _, ok := existing[id]; !ok {
				continue
			}
			ids = append(ids, id)
		}
		values.MonitorIDs = uniquePositiveIDs(ids)
	}
	if len(values.MonitorIDs) > model.MaxPublicStatusMonitors {
		s.renderSettingsPublicError(w, r, auth, http.StatusBadRequest, values, "error.publicMonitors")
		return
	}

	rawIDs, err := json.Marshal(values.MonitorIDs)
	if err != nil {
		s.log.Error("encode public monitor ids", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	enabled := "0"
	if values.Enabled {
		enabled = "1"
	}
	if err := s.store.SetSettings(r.Context(), map[string]string{
		model.SettingPublicStatusEnabled:     enabled,
		model.SettingPublicStatusTitle:       values.Title,
		model.SettingPublicStatusDescription: values.Description,
		model.SettingPublicStatusMonitorIDs:  string(rawIDs),
	}, s.now()); err != nil {
		s.log.Error("save public status settings", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	http.Redirect(w, r, "/settings/public?notice=public_status_saved", http.StatusSeeOther)
}

func (s *Server) renderSettingsPublicError(w http.ResponseWriter, r *http.Request, auth model.AuthInfo, status int, values publicStatusFormValues, key string) {
	page, err := s.loadSettingsPage(r, auth)
	if err != nil {
		s.log.Error("reload settings", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "error.internal")
		return
	}
	page.PublicStatus = values
	selected := make(map[int64]struct{}, len(values.MonitorIDs))
	for _, id := range values.MonitorIDs {
		selected[id] = struct{}{}
	}
	for index := range page.PublicMonitors {
		_, page.PublicMonitors[index].Selected = selected[page.PublicMonitors[index].ID]
	}
	page.Error = page.I18n.T(key)
	s.render(w, "settings", status, page)
}
