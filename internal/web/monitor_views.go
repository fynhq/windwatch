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

type historyBar struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
	Class  string
	Title  string
}

type historyChart struct {
	HasData    bool
	Bars       []historyBar
	StartLabel string
	EndLabel   string
	BarLabel   string
	Hint       string
	AriaLabel  string
}

type latencyPoint struct {
	X        float64
	Y        float64
	HitX     float64
	HitWidth float64
	Title    string
	Solo     bool
	Last     bool
}

type latencyChart struct {
	HasData    bool
	GradientID string
	LinePaths  []string
	AreaPaths  []string
	Points     []latencyPoint
	StartLabel string
	EndLabel   string
	Label      string
	MaxLabel   string
	MidLabel   string
	AvgY       float64
	ShowAvg    bool
	StatsLabel string
	Hint       string
	AriaLabel  string
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
	TypeShort      string
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
	Inactive       bool
	ShowInterval   bool
	ShowLatency    bool
	ShowLastCheck  bool
	RowTitle       string
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
	SummaryLabel string
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
	TypeShort         string
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
	LastError         string
	Interval          string
	Timeout           string
	FailThreshold     int
	RecoverThreshold  int
	Uptime24          string
	Uptime7d          string
	Uptime30d         string
	Uptime90d         string
	AvgLatency24      string
	RecentBarsSummary string
	BarsSummary       string
	ChartRecent       historyChart
	Chart24           historyChart
	Chart30           historyChart
	Chart90           historyChart
	LatencyRecent     latencyChart
	Latency24         latencyChart
	Latency30         latencyChart
	Latency90         latencyChart
	YearHeatmap       yearHeatmapView
	Events            []eventRow
	ProxyName         string
	NotificationLabel string
	CertEnabled       bool
	Cert              *certificateDetailView
	PushURL           string
	PushToken         string
	PushLastHeartbeat string
}

type yearHeatmapView struct {
	HasData    bool
	Months     []yearHeatmapMonth
	Rows       []yearHeatmapRow
	RangeLabel string
	Summary    string
	AriaLabel  string
}

type yearHeatmapMonth struct {
	Label string
	Span  int
}

type yearHeatmapRow struct {
	Label string
	Cells []yearHeatmapCell
}

type yearHeatmapCell struct {
	Class string
	Title string
}

type certificateDetailView struct {
	Issuer      string
	Subject     string
	Expires     string
	Days        string
	Fingerprint string
	Warning     bool
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
	TypeShort  string
	Action     string
	CancelURL  string
	Values     monitorFormValues
	Proxies    []proxyOption
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
	return monitorTypeLabel(translator, monitorType)
}

func monitorTypeLabel(translator *i18n.Translator, monitorType model.MonitorType) string {
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

func monitorTypeShortLabel(translator *i18n.Translator, monitorType model.MonitorType) string {
	switch monitorType {
	case model.MonitorTypeHTTP:
		return translator.T("monitor.typeShort.http")
	case model.MonitorTypeTCP:
		return translator.T("monitor.typeShort.tcp")
	case model.MonitorTypeDocker:
		return translator.T("monitor.typeShort.docker")
	case model.MonitorTypePush:
		return translator.T("monitor.typeShort.push")
	default:
		return string(monitorType)
	}
}

type monitorStatusView struct {
	Class    string
	Key      string
	LabelKey string
}

func monitorStatusViewFor(enabled bool, status model.MonitorStatus) monitorStatusView {
	if !enabled {
		return monitorStatusView{Class: "status-paused", Key: "paused", LabelKey: "status.paused"}
	}
	switch status {
	case model.StatusUp:
		return monitorStatusView{Class: "status-up", Key: "up", LabelKey: "status.up"}
	case model.StatusDown:
		return monitorStatusView{Class: "status-down", Key: "down", LabelKey: "status.down"}
	case model.StatusWarning:
		return monitorStatusView{Class: "status-warning", Key: "warning", LabelKey: "status.warning"}
	default:
		return monitorStatusView{Class: "status-pending", Key: "pending", LabelKey: "status.pending"}
	}
}

func statusClass(enabled bool, status model.MonitorStatus) string {
	return monitorStatusViewFor(enabled, status).Class
}

func statusLabel(translator *i18n.Translator, enabled bool, status model.MonitorStatus) string {
	return translator.T(monitorStatusViewFor(enabled, status).LabelKey)
}

func statusKey(enabled bool, status model.MonitorStatus) string {
	return monitorStatusViewFor(enabled, status).Key
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

func summarizeChecks(translator *i18n.Translator, checks []model.CheckSummary, limit int) string {
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

func checkBarRange(translator *i18n.Translator, now time.Time, checks []model.CheckSummary, limit int) (string, string) {
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
	case model.EventWarnCert:
		return translator.T("event.warn_cert")
	default:
		return string(eventType)
	}
}

func eventTypeClass(eventType model.EventType) string {
	switch eventType {
	case model.EventUp:
		return "status-up"
	case model.EventWarnCert:
		return "status-warning"
	default:
		return "status-down"
	}
}

func buildCheckBars(translator *i18n.Translator, checks []model.CheckSummary, limit int, location *time.Location) []statusBar {
	if limit <= 0 {
		limit = 24
	}
	if len(checks) > limit {
		checks = checks[:limit]
	}
	bars := make([]statusBar, limit)
	empty := statusBar{Class: "bar-empty", Title: translator.T("common.noData")}
	for index := 0; index < limit-len(checks); index++ {
		bars[index] = empty
	}
	for index, check := range checks {
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
		bars[limit-1-index] = statusBar{Class: class, Title: title}
	}
	return bars
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

func buildRecentChart(translator *i18n.Translator, checks []model.CheckSummary, limit int, location *time.Location) (historyChart, latencyChart) {
	if limit <= 0 {
		limit = 24
	}
	if len(checks) > limit {
		checks = checks[:limit]
	}
	if len(checks) == 0 {
		return historyChart{}, latencyChart{}
	}
	data := make([]chartDatum, 0, len(checks))
	for index := len(checks) - 1; index >= 0; index-- {
		check := checks[index]
		ratio := 0.0
		class := "bar-down"
		switch check.Status {
		case model.CheckUp:
			ratio = 1
			class = "bar-up"
		case model.CheckWarning:
			ratio = 1
			class = "bar-mixed"
		}
		title := formatChartMoment(translator, check.CheckedAt, location) + " · " + checkStatusLabel(translator, check.Status)
		if check.Latency > 0 {
			title += " · " + formatLatency(check.Latency)
		}
		chartLatency := check.Latency
		if check.Status == model.CheckDown {
			chartLatency = 0
		}
		data = append(data, chartDatum{
			Title: title, Class: class, HasData: true, Ratio: ratio, Latency: chartLatency,
		})
	}
	startLabel := formatChartMoment(translator, checks[len(checks)-1].CheckedAt, location)
	endLabel := formatChartMoment(translator, checks[0].CheckedAt, location)
	bars := buildHistoryChart(data, startLabel, endLabel)
	bars.BarLabel = translator.T("monitor.chartUptime")
	bars.Hint = translator.T("monitor.rangeChartHint")
	bars.AriaLabel = translator.T("monitor.recentStatus")
	latency := buildLatencyChart(translator, data, startLabel, endLabel, "lg-recent")
	latency.Label = translator.T("monitor.chartLatency")
	latency.Hint = translator.T("monitor.latencyChartHint")
	latency.AriaLabel = translator.T("monitor.recentStatus") + " · " + translator.T("monitor.chartLatency")
	return bars, latency
}

func buildHourlyChart(translator *i18n.Translator, now time.Time, buckets []model.HourBucket, location *time.Location) (historyChart, latencyChart) {
	if location == nil {
		location = time.UTC
	}
	byStart := make(map[int64]model.HourBucket, len(buckets))
	for _, bucket := range buckets {
		byStart[bucket.Start.UnixMilli()] = bucket
	}
	currentHour := now.UTC().Truncate(time.Hour)
	firstHour := currentHour.Add(-23 * time.Hour)
	data := make([]chartDatum, 0, 24)
	for index := 0; index < 24; index++ {
		hour := firstHour.Add(time.Duration(index) * time.Hour)
		label := hour.In(location).Format("15:04")
		bucket, ok := byStart[hour.UnixMilli()]
		if !ok || bucket.Total == 0 {
			data = append(data, chartDatum{Title: label + " · " + translator.T("common.noData")})
			continue
		}
		good := bucket.Up + bucket.Warning
		ratio := float64(good) / float64(bucket.Total)
		class := "bar-mixed"
		switch {
		case good == 0:
			class = "bar-down"
		case good == bucket.Total && bucket.Warning == 0:
			class = "bar-up"
		}
		title := label + " · " + formatPercent(good, bucket.Total)
		if bucket.AvgLatency > 0 {
			title += " · " + formatLatency(bucket.AvgLatency)
		}
		chartLatency := bucket.AvgLatency
		if bucket.Down > 0 {
			chartLatency = 0
		}
		data = append(data, chartDatum{
			Title: title, Class: class, HasData: true, Ratio: ratio, Latency: chartLatency,
		})
	}
	startLabel := firstHour.In(location).Format("15:04")
	endLabel := currentHour.In(location).Format("15:04")
	bars := buildHistoryChart(data, startLabel, endLabel)
	bars.BarLabel = translator.T("monitor.chartUptime")
	bars.Hint = translator.T("monitor.rangeChartHint")
	bars.AriaLabel = translator.T("monitor.history24h")
	latency := buildLatencyChart(translator, data, startLabel, endLabel, "lg-24h")
	latency.Label = translator.T("monitor.chartLatency")
	latency.Hint = translator.T("monitor.latencyChartHint")
	latency.AriaLabel = translator.T("monitor.history24h") + " · " + translator.T("monitor.chartLatency")
	return bars, latency
}

func buildDailyChart(translator *i18n.Translator, now time.Time, buckets []model.HourBucket, days int, location *time.Location, ariaKey string) (historyChart, latencyChart) {
	if days <= 0 {
		return historyChart{}, latencyChart{}
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
	data := make([]chartDatum, 0, days)
	for index := 0; index < days; index++ {
		day := start.AddDate(0, 0, index)
		dateLabel := formatChartDate(translator, day, location)
		bucket, ok := byStart[day.UnixMilli()]
		if !ok || bucket.Total == 0 {
			data = append(data, chartDatum{Title: dateLabel + " · —"})
			continue
		}
		good := bucket.Up + bucket.Warning
		ratio := float64(good) / float64(bucket.Total)
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
		}
		chartLatency := bucket.AvgLatency
		if bucket.Down > 0 {
			chartLatency = 0
		}
		data = append(data, chartDatum{
			Title: title, Class: class, HasData: true, Ratio: ratio, Latency: chartLatency,
		})
	}
	startLabel := formatChartDate(translator, start, location)
	endLabel := formatChartDate(translator, end.AddDate(0, 0, -1), location)
	bars := buildHistoryChart(data, startLabel, endLabel)
	bars.BarLabel = translator.T("monitor.chartUptime")
	bars.Hint = translator.T("monitor.dailyChartHint")
	bars.AriaLabel = translator.T(ariaKey)
	latency := buildLatencyChart(translator, data, startLabel, endLabel, fmt.Sprintf("lg-%dd", days))
	latency.Label = translator.T("monitor.chartLatency")
	latency.Hint = translator.T("monitor.latencyChartHint")
	latency.AriaLabel = translator.T(ariaKey) + " · " + translator.T("monitor.chartLatency")
	return bars, latency
}

func buildYearHeatmap(translator *i18n.Translator, now time.Time, buckets []model.HourBucket, location *time.Location) yearHeatmapView {
	const weeks = 53
	if location == nil {
		location = time.UTC
	}
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	weekdayOffset := (int(today.Weekday()) + 6) % 7
	start := today.AddDate(0, 0, -weekdayOffset-weeks*7+7)

	byDay := make(map[int64]model.HourBucket, len(buckets))
	for _, bucket := range buckets {
		local := bucket.Start.In(location)
		dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
		byDay[dayStart.UnixMilli()] = bucket
	}

	weekdayLabels := make([]string, 7)
	weekdayLabels[0] = translator.T("monitor.weekdayMonday")
	weekdayLabels[2] = translator.T("monitor.weekdayWednesday")
	weekdayLabels[4] = translator.T("monitor.weekdayFriday")
	rows := make([]yearHeatmapRow, 7)
	for index := range rows {
		rows[index].Label = weekdayLabels[index]
	}

	var (
		months       []yearHeatmapMonth
		currentMonth time.Month
		monthStart   int
		monthLabel   string
		hasData      bool
		dataDays     int
		downDays     int
		warnDays     int
		totalChecks  int
		goodChecks   int
	)

	for week := 0; week < weeks; week++ {
		weekStart := start.AddDate(0, 0, week*7)
		if week == 0 {
			currentMonth = weekStart.Month()
			monthLabel = shortMonthLabel(translator, weekStart)
			monthStart = week
		} else if weekStart.Month() != currentMonth {
			months = append(months, yearHeatmapMonth{Label: monthLabel, Span: week - monthStart})
			currentMonth = weekStart.Month()
			monthLabel = shortMonthLabel(translator, weekStart)
			monthStart = week
		}

		for day := 0; day < 7; day++ {
			date := weekStart.AddDate(0, 0, day)
			cell := yearHeatmapCell{Class: "heat-empty"}
			if date.After(today) {
				cell.Class = "heat-future"
				rows[day].Cells = append(rows[day].Cells, cell)
				continue
			}

			bucket, ok := byDay[date.UnixMilli()]
			if !ok || bucket.Total == 0 {
				cell.Title = formatChartDate(translator, date, location) + " · " + translator.T("common.noData")
				rows[day].Cells = append(rows[day].Cells, cell)
				continue
			}

			good := bucket.Up + bucket.Warning
			ratio := float64(good) / float64(bucket.Total)
			dateLabel := formatChartDate(translator, date, location)
			percent := formatPercent(good, bucket.Total)
			switch {
			case ratio < 0.95:
				cell.Class = "heat-down"
				cell.Title = dateLabel + " · " + translator.T("status.down") + " · " + percent
				downDays++
			case bucket.Warning > 0 && bucket.Down == 0:
				cell.Class = "heat-warn"
				cell.Title = dateLabel + " · " + translator.T("status.warning") + " · " + percent
				warnDays++
			case ratio < 0.99:
				cell.Class = "heat-1"
				cell.Title = dateLabel + " · " + percent
			case ratio < 0.995:
				cell.Class = "heat-2"
				cell.Title = dateLabel + " · " + percent
			case ratio < 0.999:
				cell.Class = "heat-3"
				cell.Title = dateLabel + " · " + percent
			default:
				cell.Class = "heat-4"
				cell.Title = dateLabel + " · " + percent
			}
			hasData = true
			dataDays++
			totalChecks += bucket.Total
			goodChecks += good
			rows[day].Cells = append(rows[day].Cells, cell)
		}
	}
	months = append(months, yearHeatmapMonth{Label: monthLabel, Span: weeks - monthStart})

	view := yearHeatmapView{
		HasData:    hasData,
		Months:     months,
		Rows:       rows,
		RangeLabel: start.Format("2006-01") + " — " + today.Format("2006-01"),
	}
	if !hasData || dataDays == 0 {
		view.Summary = translator.T("monitor.noLongRangeData")
		view.AriaLabel = translator.T("monitor.uptime1yChart")
		return view
	}

	parts := []string{translator.Tf("monitor.yearAvailable", formatPercent(goodChecks, totalChecks))}
	if downDays == 1 {
		parts = append(parts, translator.T("monitor.yearDownDay"))
	} else if downDays > 1 {
		parts = append(parts, translator.Tf("monitor.yearDownDays", downDays))
	}
	if warnDays == 1 {
		parts = append(parts, translator.T("monitor.yearWarnDay"))
	} else if warnDays > 1 {
		parts = append(parts, translator.Tf("monitor.yearWarnDays", warnDays))
	}
	view.Summary = strings.Join(parts, " · ")
	view.AriaLabel = translator.T("monitor.uptime1yChart") + " · " + view.Summary
	return view
}

func shortMonthLabel(translator *i18n.Translator, value time.Time) string {
	return value.Format(translator.T("date.monthShort"))
}

const (
	chartPlotLeft  = 8.0
	chartPlotRight = 712.0
	chartBaseY     = 140.0
	chartBarHeight = 118.0
)

type chartDatum struct {
	Title   string
	Class   string
	HasData bool
	Ratio   float64
	Latency time.Duration
}

func chartGeometry(count int) (step, width float64) {
	step = (chartPlotRight - chartPlotLeft) / float64(count)
	width = step * 0.62
	if width < 2 {
		width = 2
	}
	if width > 18 {
		width = 18
	}
	return step, width
}

func buildHistoryChart(data []chartDatum, startLabel, endLabel string) historyChart {
	chart := historyChart{StartLabel: startLabel, EndLabel: endLabel}
	if len(data) == 0 {
		return chart
	}
	step, width := chartGeometry(len(data))
	for index, datum := range data {
		x := chartPlotLeft + float64(index)*step
		if !datum.HasData {
			chart.Bars = append(chart.Bars, historyBar{
				X: x, Y: chartBaseY - 2, Width: width, Height: 2,
				Class: "bar-empty", Title: datum.Title,
			})
			continue
		}
		chart.HasData = true
		height := datum.Ratio * chartBarHeight
		if height < 2 {
			height = 2
		}
		chart.Bars = append(chart.Bars, historyBar{
			X: x, Y: chartBaseY - height, Width: width, Height: height,
			Class: datum.Class, Title: datum.Title,
		})
	}
	return chart
}

func niceLatencyCeil(value time.Duration) time.Duration {
	magnitude := time.Duration(1)
	for magnitude*10 <= value {
		magnitude *= 10
	}
	for _, factor := range []time.Duration{1, 2, 4, 10} {
		if magnitude*factor >= value {
			return magnitude * factor
		}
	}
	return magnitude * 10
}

func buildLatencyChart(translator *i18n.Translator, data []chartDatum, startLabel, endLabel, gradientID string) latencyChart {
	chart := latencyChart{StartLabel: startLabel, EndLabel: endLabel, GradientID: gradientID}
	var (
		maxLatency time.Duration
		minLatency time.Duration
		total      time.Duration
		count      int
	)
	for _, datum := range data {
		if !datum.HasData || datum.Latency <= 0 {
			continue
		}
		if datum.Latency > maxLatency {
			maxLatency = datum.Latency
		}
		if minLatency == 0 || datum.Latency < minLatency {
			minLatency = datum.Latency
		}
		total += datum.Latency
		count++
	}
	if count == 0 {
		return chart
	}
	scale := niceLatencyCeil(maxLatency)
	avg := total / time.Duration(count)
	step, width := chartGeometry(len(data))
	var segment strings.Builder
	segmentCount := 0
	firstX, lastX := 0.0, 0.0
	flush := func() {
		if segment.Len() == 0 {
			return
		}
		chart.LinePaths = append(chart.LinePaths, segment.String())
		chart.AreaPaths = append(chart.AreaPaths, fmt.Sprintf("%s L %.1f %.1f L %.1f %.1f Z", segment.String(), lastX, chartBaseY, firstX, chartBaseY))
		if segmentCount == 1 {
			chart.Points[len(chart.Points)-1].Solo = true
		}
		segment.Reset()
		segmentCount = 0
	}
	for index, datum := range data {
		if !datum.HasData || datum.Latency <= 0 {
			flush()
			continue
		}
		x := chartPlotLeft + float64(index)*step + width/2
		y := chartBaseY - (float64(datum.Latency)/float64(scale))*chartBarHeight
		if segment.Len() == 0 {
			fmt.Fprintf(&segment, "M %.1f %.1f", x, y)
			firstX = x
		} else {
			fmt.Fprintf(&segment, " L %.1f %.1f", x, y)
		}
		lastX = x
		segmentCount++
		chart.Points = append(chart.Points, latencyPoint{
			X: x, Y: y,
			HitX: chartPlotLeft + float64(index)*step, HitWidth: step,
			Title: datum.Title,
		})
	}
	flush()
	chart.Points[len(chart.Points)-1].Last = true
	chart.HasData = true
	chart.MaxLabel = formatLatency(scale)
	chart.MidLabel = formatLatency(scale / 2)
	chart.AvgY = chartBaseY - (float64(avg)/float64(scale))*chartBarHeight
	chart.ShowAvg = count >= 2
	chart.StatsLabel = strings.Join([]string{
		translator.Tf("monitor.latencyMinStat", formatLatency(minLatency)),
		translator.Tf("monitor.latencyAvgStat", formatLatency(avg)),
		translator.Tf("monitor.latencyMaxStat", formatLatency(maxLatency)),
	}, " · ")
	return chart
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

func formatChartMoment(translator *i18n.Translator, value time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	value = value.In(location)
	if translator.Lang() == i18n.LangZH {
		return value.Format("01-02 15:04")
	}
	return value.Format("Jan 2 15:04")
}

func buildEventRows(translator *i18n.Translator, events []model.Event, includeMonitor bool, location *time.Location, now time.Time) []eventRow {
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
			row.Duration = i18n.FormatDuration(translator, now.Sub(event.StartedAt))
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
		if monitor.HTTP.CertWarnDays > 0 {
			values.CertWarnDays = fmt.Sprintf("%d", monitor.HTTP.CertWarnDays)
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

func bucketsSince(buckets []model.HourBucket, since time.Time) []model.HourBucket {
	filtered := make([]model.HourBucket, 0, len(buckets))
	for _, bucket := range buckets {
		if !bucket.Start.Before(since) {
			filtered = append(filtered, bucket)
		}
	}
	return filtered
}

func secondsLabel(duration time.Duration) string {
	return fmt.Sprintf("%ds", int(duration/time.Second))
}
