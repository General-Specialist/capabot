-- GoStaff current schema (consolidated from migrations 001–015)
-- This file is the source of truth for the DB structure.
-- Do not apply directly — changes must still go through numbered migrations.

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    channel    TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL DEFAULT '',
    user_id    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata   JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_sessions_tenant ON sessions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user   ON sessions(tenant_id, user_id);

CREATE TABLE IF NOT EXISTS messages (
    id           BIGSERIAL PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'system', 'tool')),
    content      TEXT NOT NULL,
    tool_call_id TEXT,
    tool_name    TEXT,
    tool_input   TEXT NOT NULL DEFAULT '',
    token_count  INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);

CREATE TABLE IF NOT EXISTS tool_executions (
    id          BIGSERIAL PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    tool_name   TEXT NOT NULL,
    input       TEXT NOT NULL,
    output      TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    success     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tool_exec_session ON tool_executions(session_id);

CREATE TABLE IF NOT EXISTS memory (
    id         BIGSERIAL PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, key)
);
CREATE INDEX IF NOT EXISTS idx_memory_tenant ON memory(tenant_id);

CREATE TABLE IF NOT EXISTS automations (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    rrule       TEXT NOT NULL,
    prompt      TEXT NOT NULL,
    skill_names TEXT[] NOT NULL DEFAULT '{}',
    start_at    TIMESTAMPTZ,
    end_at      TIMESTAMPTZ,
    start_offset TEXT NOT NULL DEFAULT '',
    end_offset   TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS automation_runs (
    id            BIGSERIAL PRIMARY KEY,
    automation_id BIGINT NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ,
    status        TEXT NOT NULL DEFAULT 'running' CHECK(status IN ('running', 'success', 'error', 'stopped')),
    response      TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_automation_runs_automation ON automation_runs(automation_id);

CREATE TABLE IF NOT EXISTS people (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    prompt          TEXT NOT NULL DEFAULT '',
    username        TEXT NOT NULL DEFAULT '',
    avatar_url      TEXT NOT NULL DEFAULT '',
    avatar_position TEXT NOT NULL DEFAULT 'center',
    tags            TEXT[] NOT NULL DEFAULT '{}',
    discord_role_id TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS discord_tag_roles (
    tag     TEXT PRIMARY KEY,
    role_id TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS channel_bindings (
    channel_id      TEXT PRIMARY KEY,
    tag             TEXT NOT NULL,
    system_prompt   TEXT NOT NULL DEFAULT '',
    skill_names     TEXT[] NOT NULL DEFAULT '{}',
    model           TEXT NOT NULL DEFAULT '',
    memory_isolated BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS modes (
    name TEXT PRIMARY KEY,
    keys TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS usage_log (
    id            BIGSERIAL PRIMARY KEY,
    provider      TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    mode          TEXT NOT NULL DEFAULT 'default',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_usage_log_created ON usage_log(created_at);
CREATE INDEX IF NOT EXISTS idx_usage_log_mode    ON usage_log(mode);
