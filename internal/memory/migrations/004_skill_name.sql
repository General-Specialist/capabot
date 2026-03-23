-- Add skill_name to automations: when set, the scheduler runs this skill
-- directly instead of calling the LLM agent.
ALTER TABLE automations ADD COLUMN skill_name TEXT NOT NULL DEFAULT '';
