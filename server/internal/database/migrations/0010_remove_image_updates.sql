-- The image update feature (checks, policies, project candidates and channel
-- publishing) was removed. Its application-owned state tables are dropped.
-- Task, audit and notification delivery evidence live in their own tables and
-- are retained untouched; notification rules that reference the removed image
-- check events simply never match again.

DROP TABLE IF EXISTS project_image_actions;
DROP TABLE IF EXISTS project_image_candidates;
DROP TABLE IF EXISTS project_image_updates;
DROP TABLE IF EXISTS image_update_registry_credentials;
DROP TABLE IF EXISTS image_update_policies;
