-- Extend channel_bindings with per-channel configuration.
-- Adds system prompt override, skill filtering, model override, and memory isolation.
ALTER TABLE channel_bindings
    ADD COLUMN IF NOT EXISTS system_prompt TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS skill_names TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS memory_isolated BOOLEAN NOT NULL DEFAULT FALSE;
