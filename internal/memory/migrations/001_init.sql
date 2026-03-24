-- Capabot schema v1 (Postgres)

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
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(tenant_id, user_id);

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
    embedding  BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, key)
);
CREATE INDEX IF NOT EXISTS idx_memory_tenant ON memory(tenant_id);

CREATE TABLE IF NOT EXISTS automations (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    rrule       TEXT NOT NULL,
    start_at    TIMESTAMPTZ,
    end_at      TIMESTAMPTZ,
    prompt      TEXT NOT NULL,
    skill_name  TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS personas (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    prompt     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
