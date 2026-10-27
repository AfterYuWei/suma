-- Add encrypted manual interpolation values without changing existing configuration
-- or historical releases. NULL means no manual values for pre-upgrade evidence.
ALTER TABLE delivery_projects ADD COLUMN environment_ciphertext bytea;
ALTER TABLE delivery_releases ADD COLUMN environment_ciphertext bytea;
