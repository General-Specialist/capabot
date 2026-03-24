-- Add 'stopped' as a valid automation_runs status.
-- SQLite doesn't support ALTER CHECK, so recreate the table.

CREATE TABLE automation_runs_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    automation_id INTEGER NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    started_at    TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at   TEXT,
    status        TEXT NOT NULL DEFAULT 'running' CHECK(status IN ('running', 'success', 'error', 'stopped')),
    response      TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT ''
);

INSERT INTO automation_runs_new SELECT * FROM automation_runs;
DROP TABLE automation_runs;
ALTER TABLE automation_runs_new RENAME TO automation_runs;
