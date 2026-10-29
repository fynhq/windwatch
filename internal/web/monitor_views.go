package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/fynhq/windwatch/internal/i18n"
	"github.com/fynhq/windwatch/internal/model"
)

type statusBar struct {
	Class string
	Title string
}

type latencyPoint struct {
	X     float64
	Y     float64
	Title string
}

type chartGridLine struct {
	Y     float64
	Label string
}

type latencyChart struct {
	HasData    bool
	LinePaths  []string
	AreaPaths  []string
	Points     []latencyPoint
	Grid       []chartGridLine
	StartLabel string
	EndLabel   string
	AvgLabel   string
	PeakLabel  string
}

type longRangeBar struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
	Class  string
	Title  string
}

type longRangeChart struct {
	HasData    bool
	Bars       []longRangeBar
	Line       string
	HasLine    bool
	StartLabel string
	EndLabel   string
}

type eventRow struct {
	MonitorID   int64
	MonitorName string
	TypeLabel   string
	TypeClass   string
	Started     string
	Ended       string
	Duration    string
	Message     string
	Ongoing     bool
	SearchText  string
}

type monitorCard struct {
	ID             int64
	Name           string
	TypeLabel      string
	Target         string
	StatusClass    string
	StatusKey      string
	StatusLabel    string
	SearchText     string
	LastCheckAgo   string
	LastCheckTitle string
	LastLatency    string
	IntervalLabel  string
	Uptime24       string
	Bars           []statusBar
	BarsSummary    string
	BarsStart      string
	BarsEnd        string
}

type dashboardPage struct {
	pageData
	Monitors     []monitorCard
	Total        int
	Up           int
	Warning      int
	Down         int
	Pending      int
	Paused       int
	OverallClass string
	OverallLabel string
	HasMonitors  bool
}

type monitorDetailPage struct {
	pageData
	Monitor monitorDetailView
}

type monitorDetailView struct {
	ID                int64
	Name              string
	TypeLabel         string
	Target            string
	Method            string
	IsPush            bool
	Enabled           bool
	StatusClass       string
	StatusLabel       string
	StatusSince       string
	StatusSinceAgo    string
	LastCheckAgo      string
	LastCheckTitle    string
	LastLatency       string
	Interval          string
	Timeout           string
	FailThreshold     int
	RecoverThreshold  int
	Uptime24          string
	Uptime7d          string
	Uptime30d         string
	Uptime90d         string
	AvgLatency24      string
	RecentBars        []statusBar
	RecentBarsSummary string
	RecentBarsStart   string
	RecentBarsEnd     string
	Bars              []statusBar
	BarsSummary       string
	BarsStart         string
	BarsEnd           string
	Chart             latencyChart
	Chart30           longRangeChart
	Chart90           longRangeChart
	HasChecks         bool
	Events            []eventRow
	ProxyName         string
	NotificationLabel string
	CertEnabled       bool
	Cert              *certificateDetailView
	DomainEnabled     bool
	Domain            *domainDetailView
	PushURL           string
	PushToken         string
	PushLastHeartbeat string
}

type certificateDetailView struct {
	Issuer      string
	Subject     string
	Expires     string
	Days        string
	Fingerprint string
	Warning     bool
}

type domainDetailView struct {
	Domain    string
	Registrar string
	Expires   string
	Days      string
	Statuses  string
	Fetched   string
	Error     string
	Warning   bool
}

type eventsPage struct {
	pageData
	Events       []eventRow
	HasEvents    bool
	Total        int
	OngoingCount int
	DownCount    int
	UpCount      int
}

type monitorTypesPage struct {
	pageData
}

type monitorFormPage struct {
	pageData
	IsEdit     bool
	Type       model.MonitorType
	TypeLabel  string
	Action     string
	CancelURL  string
	Values     monitorFormValues
	Proxies    []model.Proxy
	Channels   []notificationChannelOption
	ShowHTTP   bool
	ShowTCP    bool
	ShowDocker bool
	ShowPush   bool
	ShowProxy  bool
}

type notificationChannelOption struct {
	ID        int64
	Name      string
	TypeLabel string
	Enabled   bool
	Selected  bool
}

type monitorFormValues struct {
	Name                   string
	Interval               string
	Timeout                string
	FailThreshold          string
	RecoverThreshold       string
	ProxyID                string
	ChannelIDs             []string
	WarningReminderCount   string
	WarningReminderMinutes string
	FailureReminderCount   string
	FailureReminderMinutes string
	URL                    string
	Method                 string
	ExpectedStatus         string
	Keyword                string
	FollowRedirects        bool
	TLSSkipVerify          bool
	CertCheck              bool
	CertWarnDays           string
	DomainCheck            bool
	DomainWarnDays         string
	Host                   string
	Port                   string
	DockerContainer        string
	DockerHealthy          bool
	NotifyEnabled          bool
}

func (s *Server) monitorTarget(translator *i18n.Translator, monitor model.Monitor) string {
	if monitor.Type == model.MonitorTypePush {
		return translator.T("monitor.pushTarget")
	}
	return monitor.Target()
}

func (s *Server) typeLabel(translator *i18n.Translator, monitorType model.MonitorType) string {
	switch monitorType {
	case model.MonitorTypeHTTP:
		return translator.T("monitor.type.http")
	case model.MonitorTypeTCP:
		return translator.T("monitor.type.tcp")
	case model.MonitorTypeDocker:
		return translator.T("monitor.type.docker")
	case model.MonitorTypePush:
		return translator.T("monitor.type.push")
	default:
		return string(monitorType)
	}
}

func statusClass(enabled bool, status model.MonitorStatus) string {
	if !enabled {
		return "status-paused"
	}
	switch status {
	case model.StatusUp:
		return "status-up"
	case model.StatusDown:
		return "status-down"
	case model.StatusWarning:
		return "status-warning"
	default:
		return "status-pending"
	}
}

func statusLabel(translator *i18n.Translator, enabled bool, status model.MonitorStatus) string {
	if !enabled {
		return translator.T("status.paused")
	}
	switch status {
	case model.StatusUp:
		return translator.T("status.up")
	case model.StatusDown:
		return translator.T("status.down")
	case model.StatusWarning:
		return translator.T("status.warning")
	default:
		return translator.T("status.pending")
	}
}

func statusKey(enabled bool, status model.MonitorStatus) string {
	if !enabled {
		return "paused"
	}
	switch status {
	case model.StatusUp:
		return "up"
	case model.StatusDown:
		return "down"
	case model.StatusWarning:
		return "warning"
	default:
		return "pending"
	}
}

func dashboardOverall(translator *i18n.Translator, total, up, warning, down, pending, paused int) (string, string) {
	class := "status-up"
	label := translator.T("dashboard.overallAllUp")
	switch {
	case down > 0:
		class = "status-down"
		label = translator.T("dashboard.overallDown")
	case warning > 0:
		class = "status-warning"
		label = translator.T("dashboard.overallWarning")
	case pending > 0:
		class = "status-pending"
		label = translator.T("dashboard.overallPending")
	case total > 0 && up == 0 && paused == total:
		class = "status-paused"
		label = translator.T("dashboard.overallPaused")
	}
	return class, label
}

func summarizeChecks(translator *i18n.Translator, checks []model.Check, limit int) string {
	if limit <= 0 {
		limit = 24
	}
	if len(checks) > limit {
		checks = checks[:limit]
	}
	if len(checks) == 0 {
		return translator.T("dashboard.noChecks")
	}
	var up, warning, down int
	for _, check := range checks {
		switch check.Status {
		case model.CheckUp:
			up++
		case model.CheckWarning:
			warning++
		default:
			down++
		}
	}
	return translator.Tf("dashboard.recentSummary", up, warning, down)
}

func checkBarRange(translator *i18n.Translator, now time.Time, checks []model.Check, limit int) (string, string) {
	if limit <= 0 {
		limit = 24
	}
	if len(checks) > limit {
		checks = checks[:limit]
	}
	if len(checks) == 0 {
		return translator.T("common.noData"), translator.T("common.now")
	}
	oldest := checks[len(checks)-1]
	newest := checks[0]
	return formatRelativeTime(translator, now, oldest.CheckedAt), formatRelativeTime(translator, now, newest.CheckedAt)
}

func checkStatusLabel(translator *i18n.Translator, status string) string {
	switch status {
	case model.CheckUp:
		return translator.T("status.up")
	case model.CheckWarning:
		return translator.T("status.warning")
	default:
		return translator.T("status.down")
	}
}

func eventTypeLabel(translator *i18n.Translator, eventType model.EventType) string {
	switch eventType {
	case model.EventUp:
		return translator.T("event.up")
	case model.EventDown:
		return translator.T("event.down")
	default:
		return string(eventType)
	}
}

func eventTypeClass(eventType model.EventType) string {
	if eventType == model.EventUp {
		return "status-up"
	}
	return "status-down"
}

func buildStatusBars(translator *i18n.Translator, now time.Time, buckets []model.HourBucket, location *time.Location) []statusBar {
	byStart := make(map[int64]model.HourBucket, len(buckets))
	for _, bucket := range buckets {
		byStart[bucket.Start.UnixMilli()] = bucket
	}
	currentHour := now.UTC().Truncate(time.Hour)
	firstHour := currentHour.Add(-23 * time.Hour)

	bars := make([]statusBar, 0, 24)
	for index := 0; index < 24; index++ {
		hour := firstHour.Add(time.Duration(index) * time.Hour)
		label := hour.In(location).Format("15:04")
		bucket, ok := byStart[hour.UnixMilli()]
		if !ok || bucket.Total == 0 {
			bars = append(bars, statusBar{Class: "bar-empty", Title: label + " · " + translator.T("common.noData")})
			continue
		}
		good := bucket.Up + bucket.Warning
		class := "bar-mixed"
		switch {
		case bucket.Down == bucket.Total:
			class = "bar-down"
		case good == bucket.Total:
			class = "bar-up"
		}
		bars = append(bars, statusBar{
			Class: class,
			Title: label + " · " + formatPercent(good, bucket.Total),
		})
	}
	return bars
}

func buildCheckBars(translator *i18n.Translator, checks []model.Check, limit int, location *time.Location) []statusBar {
	if limit <= 0 {
		limit = 24
	}
	if len(checks) > limit {
		checks = checks[:limit]
	}
	bars := make([]statusBar, 0, limit)
	for index := len(checks) - 1; index >= 0; index-- {
		check := checks[index]
		class := "bar-down"
		switch check.Status {
		case model.CheckUp:
			class = "bar-up"
		case model.CheckWarning:
			class = "bar-mixed"
		}
		title := formatTime(translator, check.CheckedAt, location) + " · " + checkStatusLabel(translator, check.Status)
		if check.Latency > 0 {
			title += " · " + formatLatency(check.Latency)
		}
		bars = append(bars, statusBar{Class: class, Title: title})
	}
	for len(bars) < limit {
		bars = append([]statusBar{{Class: "bar-empty", Title: translator.T("common.noData")}}, bars...)
	}
	return bars
}

func summarizeLast24Hours(now time.Time, buckets []model.HourBucket) (good, total int) {
	first := now.UTC().Truncate(time.Hour).Add(-23 * time.Hour).UnixMilli()
	for _, bucket := range buckets {
		if bucket.Start.UnixMilli() < first {
			continue
		}
		good += bucket.Up + bucket.Warning
		total += bucket.Total
	}
	return good, total
}

func summarizeHourBuckets(translator *i18n.Translator, now time.Time, buckets []model.HourBucket) string {
	byStart := make(map[int64]model.HourBucket, len(buckets))
	for _, bucket := range buckets {
		byStart[bucket.Start.UnixMilli()] = bucket
	}
	currentHour := now.UTC().Truncate(time.Hour)
	firstHour := currentHour.Add(-23 * time.Hour)
	var upHours, warningHours, downHours int
	for index := 0; index < 24; index++ {
		bucket, ok := byStart[firstHour.Add(time.Duration(index)*time.Hour).UnixMilli()]
		if !ok || bucket.Total == 0 {
			continue
		}
		switch {
		case bucket.Down == bucket.Total:
			downHours++
		case bucket.Up+bucket.Warning == bucket.Total:
			upHours++
		default:
			warningHours++
		}
	}
	return translator.Tf("monitor.hourlySummary", upHours, warningHours, downHours)
}

func hourlyBarRange(now time.Time, location *time.Location) (string, string) {
	if location == nil {
		location = time.UTC
	}
	currentHour := now.UTC().Truncate(time.Hour)
	firstHour := currentHour.Add(-23 * time.Hour)
	return firstHour.In(location).Format("15:04"), currentHour.In(location).Format("15:04")
}

func buildLatencyChart(translator *i18n.Translator, now time.Time, buckets []model.HourBucket, location *time.Location, average time.Duration) latencyChart {
	const (
		plotLeft   = 48.0
		plotRight  = 708.0
		plotTop    = 16.0
		plotBottom = 184.0
	)
	if location == nil {
		location = time.UTC
	}
	byStart := make(map[int64]model.HourBucket, len(buckets))
	maxLatency := int64(0)
	for _, bucket := range buckets {
		byStart[bucket.Start.UnixMilli()] = bucket
		if bucket.AvgLatency.Milliseconds() > maxLatency {
			maxLatency = bucket.AvgLatency.Milliseconds()
		}
	}
	if maxLatency <= 0 {
		return latencyChart{}
	}

	currentHour := now.UTC().Truncate(time.Hour)
	firstHour := currentHour.Add(-23 * time.Hour)
	step := (plotRight - plotLeft) / 24
	chart := latencyChart{
		HasData:    true,
		StartLabel: firstHour.In(location).Format("15:04"),
		EndLabel:   currentHour.In(location).Format("15:04"),
		AvgLabel:   formatLatency(average),
		PeakLabel:  formatLatency(time.Duration(maxLatency) * time.Millisecond),
	}
	for _, fraction := range []float64{0, 0.5, 1} {
		value := float64(maxLatency) * fraction
		y := plotBottom - fraction*(plotBottom-plotTop)
		chart.Grid = append(chart.Grid, chartGridLine{
			Y:     y,
			Label: fmt.Sprintf("%d ms", int64(value+0.5)),
		})
	}

	var line strings.Builder
	var area strings.Builder
	lastX := 0.0
	flush := func() {
		if line.Len() == 0 {
			return
		}
		fmt.Fprintf(&area, " L %.1f %.1f Z", lastX, plotBottom)
		chart.LinePaths = append(chart.LinePaths, line.String())
		chart.AreaPaths = append(chart.AreaPaths, area.String())
		line.Reset()
		area.Reset()
	}

	for index := 0; index < 24; index++ {
		hour := firstHour.Add(time.Duration(index) * time.Hour)
		bucket, ok := byStart[hour.UnixMilli()]
		if !ok || bucket.Total == 0 || bucket.AvgLatency <= 0 {
			flush()
			continue
		}
		x := plotLeft + step*(float64(index)+0.5)
		y := plotBottom - (float64(bucket.AvgLatency.Milliseconds())/float64(maxLatency))*(plotBottom-plotTop)
		if line.Len() == 0 {
			fmt.Fprintf(&line, "M %.1f %.1f", x, y)
			fmt.Fprintf(&area, "M %.1f %.1f L %.1f %.1f", x, plotBottom, x, y)
		} else {
			fmt.Fprintf(&line, " L %.1f %.1f", x, y)
			fmt.Fprintf(&area, " L %.1f %.1f", x, y)
		}
		lastX = x
		chart.Points = append(chart.Points, latencyPoint{
			X:     x,
			Y:     y,
			Title: hour.In(location).Format("15:04") + " · " + formatLatency(bucket.AvgLatency),
		})
	}
	flush()
	chart.HasData = len(chart.Points) > 0
	return chart
}

func buildLongRangeChart(translator *i18n.Translator, now time.Time, buckets []model.HourBucket, days int, location *time.Location) longRangeChart {
	if days <= 0 {
		return longRangeChart{}
	}
	if location == nil {
		location = time.UTC
	}
	byStart := make(map[int64]model.HourBucket, len(buckets))
	for _, bucket := range buckets {
		byStart[bucket.Start.UnixMilli()] = bucket
	}

	localNow := now.In(location)
	end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	start := end.AddDate(0, 0, -days)
	step := 704.0 / float64(days)
	bars := make([]longRangeBar, 0, days)
	type latencyPoint struct {
		x       float64
		latency float64
	}
	points := make([]latencyPoint, 0, days)
	maxLatency := int64(0)
	hasData := false

	for index := 0; index < days; index++ {
		day := start.AddDate(0, 0, index)
		x := 8 + float64(index)*step
		width := step * 0.62
		if width < 2 {
			width = 2
		}
		if width > 18 {
			width = 18
		}
		dateLabel := formatChartDate(translator, day, location)
		bucket, ok := byStart[day.UnixMilli()]
		if !ok || bucket.Total == 0 {
			bars = append(bars, longRangeBar{
				X: x, Y: 138, Width: width, Height: 2,
				Class: "bar-empty", Title: dateLabel + " · —",
			})
			continue
		}
		hasData = true
		good := bucket.Up + bucket.Warning
		ratio := float64(good) / float64(bucket.Total)
		height := ratio * 118
		if height < 2 {
			height = 2
		}
		class := "bar-up"
		switch {
		case ratio < 0.95:
			class = "bar-down"
		case ratio < 0.999:
			class = "bar-mixed"
		}
		title := dateLabel + " · " + translator.Tf("monitor.uptimePercent", int(ratio*100+0.5))
		if bucket.AvgLatency > 0 {
			title += " · " + formatLatency(bucket.AvgLatency)
			if milliseconds := bucket.AvgLatency.Milliseconds(); milliseconds > maxLatency {
				maxLatency = milliseconds
			}
		}
		bars = append(bars, longRangeBar{
			X: x, Y: 140 - height, Width: width, Height: height,
			Class: class, Title: title,
		})
		points = append(points, latencyPoint{
			x:       x + width/2,
			latency: float64(bucket.AvgLatency.Milliseconds()),
		})
	}

	var line strings.Builder
	if maxLatency > 0 {
		for _, point := range points {
			if point.latency <= 0 {
				continue
			}
			y := 140 - (point.latency/float64(maxLatency))*118
			if y < 22 {
				y = 22
			}
			if line.Len() > 0 {
				line.WriteByte(' ')
			}
			fmt.Fprintf(&line, "%.1f,%.1f", point.x, y)
		}
	}
	return longRangeChart{
		HasData:    hasData,
		Bars:       bars,
		Line:       line.String(),
		HasLine:    line.Len() > 0,
		StartLabel: formatChartDate(translator, start, location),
		EndLabel:   formatChartDate(translator, end.AddDate(0, 0, -1), location),
	}
}

func formatChartDate(translator *i18n.Translator, value time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	value = value.In(location)
	if translator.Lang() == i18n.LangZH {
		return value.Format("01-02")
	}
	return value.Format("Jan 2")
}

func buildEventRows(translator *i18n.Translator, events []model.Event, includeMonitor bool, location *time.Location) []eventRow {
	rows := make([]eventRow, 0, len(events))
	for _, event := range events {
		row := eventRow{
			MonitorID: event.MonitorID,
			TypeLabel: eventTypeLabel(translator, event.Type),
			TypeClass: eventTypeClass(event.Type),
			Started:   formatTime(translator, event.StartedAt, location),
			Message:   event.Message,
		}
		if includeMonitor {
			row.MonitorName = event.MonitorName
		}
		if event.EndedAt != nil {
			row.Ended = formatTime(translator, *event.EndedAt, location)
			row.Duration = i18n.FormatDuration(translator, event.EndedAt.Sub(event.StartedAt))
		} else if event.Type == model.EventDown {
			row.Ended = translator.T("events.ongoing")
			row.Duration = i18n.FormatDuration(translator, time.Since(event.StartedAt))
			row.Ongoing = true
		}
		row.SearchText = strings.ToLower(row.MonitorName + " " + row.TypeLabel + " " + row.Message + " " + row.Started + " " + row.Duration)
		rows = append(rows, row)
	}
	return rows
}

func defaultFormValues() monitorFormValues {
	policy := model.DefaultNotificationPolicy()
	return monitorFormValues{
		Interval:               "60",
		Timeout:                "10",
		FailThreshold:          "2",
		RecoverThreshold:       "1",
		WarningReminderCount:   fmt.Sprintf("%d", policy.WarningReminderCount),
		WarningReminderMinutes: fmt.Sprintf("%d", int(policy.WarningReminderInterval/time.Minute)),
		FailureReminderCount:   fmt.Sprintf("%d", policy.FailureReminderCount),
		FailureReminderMinutes: fmt.Sprintf("%d", int(policy.FailureReminderInterval/time.Minute)),
		Method:                 "GET",
		ExpectedStatus:         "200-299",
		FollowRedirects:        true,
		CertCheck:              true,
		CertWarnDays:           "14",
		DomainWarnDays:         "30",
		DockerHealthy:          true,
		NotifyEnabled:          true,
	}
}

func formValuesFromMonitor(monitor model.Monitor) monitorFormValues {
	policy := monitor.Reminders.Normalized()
	values := monitorFormValues{
		Name:                   monitor.Name,
		Interval:               fmt.Sprintf("%d", int(monitor.Interval/time.Second)),
		Timeout:                fmt.Sprintf("%d", int(monitor.Timeout/time.Second)),
		FailThreshold:          fmt.Sprintf("%d", monitor.FailThreshold),
		RecoverThreshold:       fmt.Sprintf("%d", monitor.RecoverThreshold),
		WarningReminderCount:   fmt.Sprintf("%d", policy.WarningReminderCount),
		WarningReminderMinutes: fmt.Sprintf("%d", int(policy.WarningReminderInterval/time.Minute)),
		FailureReminderCount:   fmt.Sprintf("%d", policy.FailureReminderCount),
		FailureReminderMinutes: fmt.Sprintf("%d", int(policy.FailureReminderInterval/time.Minute)),
		CertWarnDays:           "14",
		DomainWarnDays:         "30",
		DockerHealthy:          true,
		NotifyEnabled:          monitor.NotifyEnabled,
	}
	if monitor.ProxyID != nil {
		values.ProxyID = fmt.Sprintf("%d", *monitor.ProxyID)
	}
	for _, channelID := range monitor.NotificationChannelIDs {
		values.ChannelIDs = append(values.ChannelIDs, fmt.Sprintf("%d", channelID))
	}
	if monitor.HTTP != nil {
		values.URL = monitor.HTTP.URL
		values.Method = monitor.HTTP.Method
		values.ExpectedStatus = monitor.HTTP.ExpectedStatus
		values.Keyword = monitor.HTTP.Keyword
		values.FollowRedirects = monitor.HTTP.FollowRedirects
		values.TLSSkipVerify = monitor.HTTP.TLSSkipVerify
		values.CertCheck = monitor.HTTP.CertCheck
		values.DomainCheck = monitor.HTTP.DomainCheck
		if monitor.HTTP.CertWarnDays > 0 {
			values.CertWarnDays = fmt.Sprintf("%d", monitor.HTTP.CertWarnDays)
		}
		if monitor.HTTP.DomainWarnDays > 0 {
			values.DomainWarnDays = fmt.Sprintf("%d", monitor.HTTP.DomainWarnDays)
		}
	}
	if monitor.TCP != nil {
		values.Host = monitor.TCP.Host
		values.Port = fmt.Sprintf("%d", monitor.TCP.Port)
	}
	if monitor.Docker != nil {
		values.DockerContainer = monitor.Docker.Container
		values.DockerHealthy = monitor.Docker.RequireHealthy
	}
	return values
}

func secondsLabel(duration time.Duration) string {
	return fmt.Sprintf("%ds", int(duration/time.Second))
}

func combineUptimeStats(recent, older model.UptimeStats) model.UptimeStats {
	total := recent.Total + older.Total
	combined := model.UptimeStats{
		Total:   total,
		Up:      recent.Up + older.Up,
		Warning: recent.Warning + older.Warning,
		Down:    recent.Down + older.Down,
	}
	if total > 0 {
		weighted := recent.AvgLatency*time.Duration(recent.Total) + older.AvgLatency*time.Duration(older.Total)
		combined.AvgLatency = weighted / time.Duration(total)
	}
	return combined
}
