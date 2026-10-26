-- Preserve existing delivery behavior while allowing projects to pause triggers.
ALTER TABLE delivery_projects ADD COLUMN automation_enabled boolean NOT NULL DEFAULT true;
