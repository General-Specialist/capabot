ALTER TABLE automations
  ADD COLUMN IF NOT EXISTS skill_names TEXT[] NOT NULL DEFAULT '{}';

UPDATE automations
  SET skill_names = ARRAY[skill_name]
  WHERE skill_name != '';

ALTER TABLE automations DROP COLUMN IF EXISTS skill_name;
