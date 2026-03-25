CREATE TABLE IF NOT EXISTS usage_log (
    id BIGSERIAL PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'default',
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_usage_log_created ON usage_log(created_at);
CREATE INDEX IF NOT EXISTS idx_usage_log_mode ON usage_log(mode);
