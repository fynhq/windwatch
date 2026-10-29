-- Webhook delivery formats. Existing rows keep the generic JSON payload.
ALTER TABLE notification_channels
    ADD COLUMN webhook_format TEXT NOT NULL DEFAULT 'generic'
    CHECK (webhook_format IN ('generic', 'dingtalk', 'wecom', 'feishu'));
