-- Old evidence has no pinned image identity. Do not infer it from today's tags.
ALTER TABLE delivery_projects ADD COLUMN initial_application_required boolean NOT NULL DEFAULT false;
ALTER TABLE delivery_records ADD COLUMN frozen_input_ciphertext bytea;
ALTER TABLE delivery_records ADD COLUMN images_pinned boolean NOT NULL DEFAULT false;
ALTER TABLE project_cd_records ADD COLUMN images_pinned boolean NOT NULL DEFAULT false;
CREATE TABLE release_plans (
 id varchar(32) PRIMARY KEY,
 kind varchar(16) NOT NULL CHECK (kind IN ('batch','project')),
 owner_id bigint NOT NULL,
 version text NOT NULL,
 payload_ciphertext bytea NOT NULL,
 preview_json text NOT NULL,
 preparation_task_id varchar(36) NOT NULL,
 task_id varchar(36) NOT NULL DEFAULT '',
 record_id bigint,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL
);
CREATE INDEX idx_release_plans_owner ON release_plans(kind,owner_id);
CREATE INDEX idx_release_plans_task ON release_plans(task_id);
