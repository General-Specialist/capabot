ALTER TABLE automations RENAME COLUMN cron TO rrule;
ALTER TABLE automations ADD COLUMN start_at TEXT;
ALTER TABLE automations ADD COLUMN end_at TEXT;
