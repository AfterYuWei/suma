-- Existing PostgreSQL installations keep their application state when adding
-- per-conversation delivery evidence. Fresh databases already have this column.
ALTER TABLE notification_deliveries ADD COLUMN IF NOT EXISTS chat_id varchar(128);
