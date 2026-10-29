package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

func TestNotificationChannelsCreateEditTest(t *testing.T) {
	srv, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	proxyID, err := st.CreateProxy(t.Context(), model.Proxy{
		Name: "Telegram proxy", Type: model.ProxyHTTP, Address: "127.0.0.1:7890",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateProxy: %v", err)
	}

	response, err := client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":        {csrf},
		"name":              {"Ops Telegram"},
		"type":              {"telegram"},
		"language":          {"en"},
		"telegram_token":    {"123456:bot-token"},
		"telegram_chat_ids": {"1001, 1002"},
		"proxy_id":          {strconv.FormatInt(proxyID, 10)},
		"enabled":           {"1"},
	})
	if err != nil {
		t.Fatalf("POST telegram channel: %v", err)
	}
	_ = readResponseBody(t, response)

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":     {csrf},
		"name":           {"Alerts Hook"},
		"type":           {"webhook"},
		"language":       {"zh-CN"},
		"webhook_url":    {"https://example.com/hook"},
		"webhook_format": {"dingtalk"},
		"webhook_secret": {"hook-secret"},
		"enabled":        {"1"},
	})
	if err != nil {
		t.Fatalf("POST webhook channel: %v", err)
	}
	_ = readResponseBody(t, response)

	channels, err := st.ListNotificationChannels(t.Context())
	if err != nil || len(channels) != 2 {
		t.Fatalf("channels=%+v err=%v", channels, err)
	}
	var telegram, webhook model.NotificationChannel
	for _, channel := range channels {
		switch channel.Type {
		case model.NotificationChannelTelegram:
			telegram = channel
		case model.NotificationChannelWebhook:
			webhook = channel
		}
	}
	if telegram.Name != "Ops Telegram" || telegram.TelegramChatIDs != "1001, 1002" ||
		telegram.ProxyID == nil || *telegram.ProxyID != proxyID || !telegram.Enabled ||
		telegram.Language != model.NotificationLanguageEN {
		t.Fatalf("unexpected telegram channel: %+v", telegram)
	}
	if telegram.SecretEnc == "" || telegram.SecretEnc == "123456:bot-token" {
		t.Fatal("telegram token was stored in plaintext")
	}
	if decoded, err := srv.cipher.Decrypt(telegram.SecretEnc); err != nil || decoded != "123456:bot-token" {
		t.Fatalf("decrypted token=%q err=%v", decoded, err)
	}
	if webhook.Name != "Alerts Hook" || webhook.WebhookURL != "https://example.com/hook" || !webhook.Enabled ||
		webhook.Language != model.NotificationLanguageZH || webhook.WebhookFormat != model.WebhookFormatDingTalk {
		t.Fatalf("unexpected webhook channel: %+v", webhook)
	}
	if webhook.SecretEnc == "" || webhook.SecretEnc == "hook-secret" {
		t.Fatal("webhook secret was stored in plaintext")
	}

	response, err = client.Get(testServer.URL + "/settings/notifications")
	if err != nil {
		t.Fatalf("GET notification settings: %v", err)
	}
	body := readResponseBody(t, response)
	for _, want := range []string{
		"Ops Telegram", "Alerts Hook", "Webhook · DingTalk",
		`data-dialog-open="notification-channel"`,
		`<dialog class="dialog" id="notification-channel"`,
		`data-channel-type-select`,
		`data-webhook-format-select`,
		`name="webhook_format"`,
		`value="dingtalk"`,
		`name="language"`,
		`/settings/notifications/channels/` + strconv.FormatInt(telegram.ID, 10) + `/test`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("notification page missing %q", want)
		}
	}
	if !strings.Contains(body, "Notification methods") {
		t.Fatalf("notification list heading missing: %s", body)
	}

	response, err = client.Get(testServer.URL + "/settings/notifications?channel=" + strconv.FormatInt(telegram.ID, 10))
	if err != nil {
		t.Fatalf("GET channel edit: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, `data-auto-open="true"`) ||
		!strings.Contains(body, `value="Ops Telegram"`) ||
		!strings.Contains(body, `value="1001, 1002"`) ||
		!strings.Contains(body, `<option value="en" selected>English</option>`) {
		t.Fatalf("channel edit dialog is missing values: %s", body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels/"+strconv.FormatInt(telegram.ID, 10), url.Values{
		"csrf_token":        {csrf},
		"name":              {"Ops Telegram"},
		"type":              {"telegram"},
		"language":          {"zh-CN"},
		"telegram_chat_ids": {"1001, 1002"},
		"proxy_id":          {strconv.FormatInt(proxyID, 10)},
		"enabled":           {"1"},
	})
	if err != nil {
		t.Fatalf("POST channel update: %v", err)
	}
	_ = readResponseBody(t, response)
	updated, err := st.NotificationChannelByID(t.Context(), telegram.ID)
	if err != nil || updated.Language != model.NotificationLanguageZH {
		t.Fatalf("updated channel language = %q err=%v", updated.Language, err)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels/"+strconv.FormatInt(telegram.ID, 10)+"/test", url.Values{
		"csrf_token": {csrf},
	})
	if err != nil {
		t.Fatalf("POST telegram test: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Notification test message sent.") {
		t.Fatalf("test body=%s", body)
	}
	notifier := srv.notifier.(*fakeNotifier)
	if len(notifier.tested) != 1 || notifier.tested[0] != telegram.ID {
		t.Fatalf("tested channels=%v", notifier.tested)
	}

	notifier.err = errors.New("telegram rejected")
	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels/"+strconv.FormatInt(webhook.ID, 10)+"/test", url.Values{
		"csrf_token": {csrf},
	})
	if err != nil {
		t.Fatalf("POST webhook test: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Notification test failed. Check the server log.") {
		t.Fatalf("failed test body=%s", body)
	}
}

func TestNotificationChannelValidation(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	if _, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name: "Existing", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/hook",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}

	response, err := client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":  {csrf},
		"name":        {"existing"},
		"type":        {"webhook"},
		"webhook_url": {"https://example.com/other"},
		"enabled":     {"1"},
	})
	if err != nil {
		t.Fatalf("POST duplicate channel: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "That notification method name is already in use.") {
		t.Fatalf("duplicate status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":  {csrf},
		"name":        {"Bad language"},
		"type":        {"webhook"},
		"language":    {"fr"},
		"webhook_url": {"https://example.com/other"},
		"enabled":     {"1"},
	})
	if err != nil {
		t.Fatalf("POST invalid language: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "Choose a valid notification language.") {
		t.Fatalf("invalid language status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token": {csrf},
		"name":       {"No token"},
		"type":       {"telegram"},
		"enabled":    {"1"},
	})
	if err != nil {
		t.Fatalf("POST missing token: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "A Telegram bot token is required.") {
		t.Fatalf("missing token status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":        {csrf},
		"name":              {"Bad chat"},
		"type":              {"telegram"},
		"telegram_token":    {"token"},
		"telegram_chat_ids": {"not a chat"},
		"enabled":           {"1"},
	})
	if err != nil {
		t.Fatalf("POST bad chat id: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "Enter at least one valid Telegram chat ID.") {
		t.Fatalf("bad chat status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":  {csrf},
		"name":        {"Bad URL"},
		"type":        {"webhook"},
		"webhook_url": {"ftp://example.com/hook"},
		"enabled":     {"1"},
	})
	if err != nil {
		t.Fatalf("POST invalid webhook: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "Webhook URL must be a valid http or https URL.") {
		t.Fatalf("invalid webhook status=%d body=%s", response.StatusCode, body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":        {csrf},
		"name":              {"Bad proxy"},
		"type":              {"telegram"},
		"telegram_token":    {"token"},
		"telegram_chat_ids": {"1001"},
		"proxy_id":          {"9999"},
		"enabled":           {"1"},
	})
	if err != nil {
		t.Fatalf("POST invalid proxy: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "The selected outbound proxy no longer exists.") {
		t.Fatalf("invalid proxy status=%d body=%s", response.StatusCode, body)
	}

	channels, err := st.ListNotificationChannels(t.Context())
	if err != nil || len(channels) != 1 {
		t.Fatalf("invalid channels were stored: %+v err=%v", channels, err)
	}
}

func TestNotificationChannelDeleteAndDisable(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name: "Disabled hook", Type: model.NotificationChannelWebhook, Enabled: false,
		WebhookURL: "https://example.com/hook",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}

	response, err := client.Get(testServer.URL + "/settings/notifications")
	if err != nil {
		t.Fatalf("GET notifications: %v", err)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "Disabled hook") || !strings.Contains(body, ">Disabled</span>") {
		t.Fatalf("disabled channel status missing: %s", body)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels/"+strconv.FormatInt(channelID, 10)+"/delete", url.Values{
		"csrf_token": {csrf},
	})
	if err != nil {
		t.Fatalf("POST delete channel: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Notification method deleted.") {
		t.Fatalf("delete notice missing: %s", body)
	}
	if _, err := st.NotificationChannelByID(t.Context(), channelID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("channel after delete err=%v", err)
	}
}

func TestFirstNotificationChannelAdoptsUnlinkedMonitors(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	monitorID, err := st.CreateMonitor(t.Context(), model.Monitor{
		Name: "Existing monitor", Type: model.MonitorTypeHTTP,
		HTTP:     &model.HTTPConfig{URL: "https://example.com", Method: "GET", ExpectedStatus: "200-299"},
		Interval: time.Minute, Timeout: 10 * time.Second,
		FailThreshold: 1, RecoverThreshold: 1, Enabled: true, NotifyEnabled: true,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	postChannel := func(name, targetURL string) {
		response, err := client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
			"csrf_token":  {csrf},
			"name":        {name},
			"type":        {"webhook"},
			"webhook_url": {targetURL},
			"enabled":     {"1"},
		})
		if err != nil {
			t.Fatalf("POST channel %s: %v", name, err)
		}
		_ = readResponseBody(t, response)
	}
	postChannel("First hook", "https://example.com/first")
	postChannel("Second hook", "https://example.com/second")

	monitor, err := st.MonitorByID(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("MonitorByID: %v", err)
	}
	if len(monitor.NotificationChannelIDs) != 1 {
		t.Fatalf("first channel adoption = %v, want exactly the first channel", monitor.NotificationChannelIDs)
	}
	channel, err := st.NotificationChannelByID(t.Context(), monitor.NotificationChannelIDs[0])
	if err != nil || channel.Name != "First hook" {
		t.Fatalf("adopted channel = %+v err=%v", channel, err)
	}
}

func TestNotificationWebhookFormats(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":     {csrf},
		"name":           {"WeCom hook"},
		"type":           {"webhook"},
		"language":       {"zh-CN"},
		"webhook_url":    {"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test"},
		"webhook_format": {"wecom"},
		"webhook_secret": {"ignored-secret"},
		"enabled":        {"1"},
	})
	if err != nil {
		t.Fatalf("POST WeCom channel: %v", err)
	}
	_ = readResponseBody(t, response)

	channels, err := st.ListNotificationChannels(t.Context())
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels=%+v err=%v", channels, err)
	}
	if channels[0].WebhookFormat != model.WebhookFormatWeCom || channels[0].SecretEnc != "" {
		t.Fatalf("wecom channel = %+v", channels[0])
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":     {csrf},
		"name":           {"DingTalk hook"},
		"type":           {"webhook"},
		"webhook_url":    {"https://oapi.dingtalk.com/robot/send?access_token=test"},
		"webhook_format": {"dingtalk"},
		"webhook_secret": {"ding-secret"},
		"enabled":        {"1"},
	})
	if err != nil {
		t.Fatalf("POST DingTalk channel: %v", err)
	}
	_ = readResponseBody(t, response)
	channels, err = st.ListNotificationChannels(t.Context())
	if err != nil || len(channels) != 2 {
		t.Fatalf("channels after DingTalk create=%+v err=%v", channels, err)
	}
	dingTalk := channels[1]
	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels/"+strconv.FormatInt(dingTalk.ID, 10), url.Values{
		"csrf_token":     {csrf},
		"name":           {dingTalk.Name},
		"type":           {"webhook"},
		"webhook_url":    {dingTalk.WebhookURL},
		"webhook_format": {"feishu"},
		"enabled":        {"1"},
	})
	if err != nil {
		t.Fatalf("POST format change: %v", err)
	}
	_ = readResponseBody(t, response)
	updated, err := st.NotificationChannelByID(t.Context(), dingTalk.ID)
	if err != nil || updated.WebhookFormat != model.WebhookFormatFeishu || updated.SecretEnc == "" {
		t.Fatalf("format change lost the secret: %+v err=%v", updated, err)
	}

	response, err = client.PostForm(testServer.URL+"/settings/notifications/channels", url.Values{
		"csrf_token":     {csrf},
		"name":           {"Bad format hook"},
		"type":           {"webhook"},
		"webhook_url":    {"https://example.com/hook"},
		"webhook_format": {"slack"},
		"enabled":        {"1"},
	})
	if err != nil {
		t.Fatalf("POST invalid webhook format: %v", err)
	}
	body := readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Choose Generic, DingTalk, WeCom, or Feishu") {
		t.Fatalf("invalid webhook format status=%d body=%s", response.StatusCode, body)
	}
}

func TestNotificationDeliveryLogIsLocalized(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, _ := setupAdminSession(t, testServer)

	now := time.Now().UTC()
	channel := model.NotificationChannel{
		Name: "Primary hook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/hook",
	}
	channelID, err := st.CreateNotificationChannel(t.Context(), channel, now)
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}
	claimed, err := st.ClaimNotification(t.Context(), "delivery-test", string(channel.Type), channelID, channel.Name, "https://example.com/hook", `{"version":1,"event":"down","text":"delivery-body"}`, now)
	if err != nil || !claimed {
		t.Fatalf("ClaimNotification claimed=%v err=%v", claimed, err)
	}
	if err := st.CompleteNotification(t.Context(), "delivery-test", string(channel.Type), channelID, "https://example.com/hook", "sent", "", now); err != nil {
		t.Fatalf("CompleteNotification: %v", err)
	}

	response, err := client.Get(testServer.URL + "/settings/logs")
	if err != nil {
		t.Fatalf("GET settings logs: %v", err)
	}
	body := readResponseBody(t, response)
	for _, want := range []string{
		"Primary hook", "Webhook", ">Sent</td>",
		`data-notification-content`, `data-notification-content-dialog`,
		`data-notification-content-field="payload"`, "delivery-body",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("delivery log missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `class="inline-details notification-content"`) {
		t.Fatal("delivery log still renders the inline details fallback")
	}

	response, err = client.Get(testServer.URL + "/language/zh-CN?next=%2Fsettings%2Flogs")
	if err != nil {
		t.Fatalf("switch language: %v", err)
	}
	_ = readResponseBody(t, response)
	response, err = client.Get(testServer.URL + "/settings/logs")
	if err != nil {
		t.Fatalf("GET settings logs in Chinese: %v", err)
	}
	body = readResponseBody(t, response)
	for _, want := range []string{"Primary hook", "Webhook", ">已发送</td>", "查看内容"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Chinese delivery log missing %q: %s", want, body)
		}
	}
}

func TestMonitorReminderConfigPerType(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	response, err := client.Get(testServer.URL + "/monitors/new?type=http")
	if err != nil {
		t.Fatalf("GET HTTP monitor form: %v", err)
	}
	body := readResponseBody(t, response)
	for _, want := range []string{
		`name="failure_reminder_count"`,
		`name="failure_reminder_interval_minutes"`,
		`name="warning_reminder_count"`,
		`name="warning_reminder_interval_minutes"`,
		`name="cert_check"`,
		`name="domain_check"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HTTP monitor form missing %q", want)
		}
	}

	response, err = client.Get(testServer.URL + "/monitors/new?type=tcp")
	if err != nil {
		t.Fatalf("GET TCP monitor form: %v", err)
	}
	body = readResponseBody(t, response)
	for _, want := range []string{`name="failure_reminder_count"`, `name="failure_reminder_interval_minutes"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("TCP monitor form missing %q", want)
		}
	}
	for _, unwanted := range []string{`name="warning_reminder_count"`, `name="cert_check"`, `name="domain_check"`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("TCP monitor form should not contain %q", unwanted)
		}
	}

	form := url.Values{
		"csrf_token":                        {csrf},
		"type":                              {"http"},
		"name":                              {"Reminder monitor"},
		"url":                               {"https://example.com"},
		"method":                            {"GET"},
		"expected_status":                   {"200-299"},
		"interval":                          {"60"},
		"timeout":                           {"10"},
		"fail_threshold":                    {"2"},
		"recover_threshold":                 {"1"},
		"notify_enabled":                    {"1"},
		"failure_reminder_count":            {"3"},
		"failure_reminder_interval_minutes": {"15"},
		"warning_reminder_count":            {"2"},
		"warning_reminder_interval_minutes": {"60"},
	}
	response, err = client.PostForm(testServer.URL+"/monitors", form)
	if err != nil {
		t.Fatalf("POST monitor with reminders: %v", err)
	}
	_ = readResponseBody(t, response)

	monitors, err := st.ListMonitors(t.Context())
	if err != nil || len(monitors) != 1 {
		t.Fatalf("monitors=%+v err=%v", monitors, err)
	}
	reminders := monitors[0].Reminders
	if reminders.FailureReminderCount != 3 || reminders.FailureReminderInterval != 15*time.Minute ||
		reminders.WarningReminderCount != 2 || reminders.WarningReminderInterval != time.Hour {
		t.Fatalf("stored reminders = %+v", reminders)
	}

	form.Set("name", "Invalid reminders")
	form.Set("failure_reminder_count", "101")
	response, err = client.PostForm(testServer.URL+"/monitors", form)
	if err != nil {
		t.Fatalf("POST invalid reminders: %v", err)
	}
	body = readResponseBody(t, response)
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(body, "Reminder count must be between 0 and 100.") {
		t.Fatalf("invalid reminder status=%d body=%s", response.StatusCode, body)
	}
}

func TestMonitorSelectsNotificationChannels(t *testing.T) {
	_, st, testServer := newWebTestServer(t)
	client, csrf := setupAdminSession(t, testServer)

	channelID, err := st.CreateNotificationChannel(t.Context(), model.NotificationChannel{
		Name: "Ops hook", Type: model.NotificationChannelWebhook, Enabled: true,
		WebhookURL: "https://example.com/hook",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateNotificationChannel: %v", err)
	}

	response, err := client.Get(testServer.URL + "/monitors/new?type=http")
	if err != nil {
		t.Fatalf("GET monitor form: %v", err)
	}
	body := readResponseBody(t, response)
	if !strings.Contains(body, "Ops hook") || !strings.Contains(body, `name="notification_channel_ids"`) {
		t.Fatalf("monitor form missing channels: %s", body)
	}

	response, err = client.PostForm(testServer.URL+"/monitors", url.Values{
		"csrf_token":               {csrf},
		"type":                     {"http"},
		"name":                     {"Channel monitor"},
		"url":                      {"https://example.com"},
		"method":                   {"GET"},
		"expected_status":          {"200-299"},
		"interval":                 {"60"},
		"timeout":                  {"10"},
		"fail_threshold":           {"2"},
		"recover_threshold":        {"1"},
		"notify_enabled":           {"1"},
		"notification_channel_ids": {strconv.FormatInt(channelID, 10)},
	})
	if err != nil {
		t.Fatalf("POST monitor: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "Channel monitor") {
		t.Fatalf("monitor detail body=%s", body)
	}
	monitors, err := st.ListMonitors(t.Context())
	if err != nil || len(monitors) != 1 {
		t.Fatalf("monitors=%+v err=%v", monitors, err)
	}
	if len(monitors[0].NotificationChannelIDs) != 1 || monitors[0].NotificationChannelIDs[0] != channelID {
		t.Fatalf("monitor channels=%v", monitors[0].NotificationChannelIDs)
	}
	if !strings.Contains(body, "Ops hook") {
		t.Fatalf("monitor detail missing notification channel: %s", body)
	}

	response, err = client.PostForm(testServer.URL+"/monitors/"+strconv.FormatInt(monitors[0].ID, 10), url.Values{
		"csrf_token":               {csrf},
		"name":                     {"Channel monitor"},
		"url":                      {"https://example.com"},
		"method":                   {"GET"},
		"expected_status":          {"200-299"},
		"interval":                 {"60"},
		"timeout":                  {"10"},
		"fail_threshold":           {"2"},
		"recover_threshold":        {"1"},
		"notify_enabled":           {"1"},
		"notification_channel_ids": {"9999"},
	})
	if err != nil {
		t.Fatalf("POST invalid channel: %v", err)
	}
	body = readResponseBody(t, response)
	if !strings.Contains(body, "The notification method no longer exists.") {
		t.Fatalf("invalid channel error missing: %s", body)
	}
}
