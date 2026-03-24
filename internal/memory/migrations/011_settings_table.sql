CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- Migrate existing system_prompt from personas to settings.
INSERT INTO settings (key, value)
SELECT 'system_prompt', COALESCE(system_prompt, '')
FROM personas LIMIT 1
ON CONFLICT (key) DO NOTHING;

ALTER TABLE personas DROP COLUMN IF EXISTS system_prompt;
