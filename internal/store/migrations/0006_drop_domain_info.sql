-- Domain expiry checks were removed. Existing installations may still have
-- cached RDAP rows, so drop the table while keeping the migration additive.
DROP TABLE IF EXISTS domain_info;

-- Drop unexpanded notifications for the removed event type; their payload can
-- no longer be rendered after the Domain field was removed.
DELETE FROM notification_outbox WHERE payload_json LIKE '%"Kind":"warn_domain"%';
