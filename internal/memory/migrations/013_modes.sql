CREATE TABLE IF NOT EXISTS modes (
    name TEXT PRIMARY KEY,
    keys TEXT NOT NULL DEFAULT '{}'
);

-- Seed default modes. Keys are empty JSON = inherit from config.yaml.
INSERT INTO modes (name, keys) VALUES ('default', '{}') ON CONFLICT DO NOTHING;
INSERT INTO modes (name, keys) VALUES ('chat', '{}') ON CONFLICT DO NOTHING;
INSERT INTO modes (name, keys) VALUES ('execute', '{}') ON CONFLICT DO NOTHING;
