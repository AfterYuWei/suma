-- Task Center module: the control plane for scheduled tasks and per-run
-- result artifacts. Plans are keyed by task kind so any feature can attach a
-- schedule without its own policy tables. Task rows gain recorded input
-- parameters and structured output results; existing evidence is untouched.

ALTER TABLE tasks ADD COLUMN params jsonb;
ALTER TABLE tasks ADD COLUMN result jsonb;

CREATE TABLE task_center_schedules (
 id bigserial PRIMARY KEY,
 name varchar(120) NOT NULL,
 kind varchar(64) NOT NULL,
 node_id varchar(64) NOT NULL DEFAULT '',
 node_name varchar(128) NOT NULL DEFAULT '',
 subject varchar(200) NOT NULL DEFAULT '',
 params jsonb NOT NULL DEFAULT '{}',
 cron varchar(64) NOT NULL,
 timezone varchar(64) NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 version bigint NOT NULL DEFAULT 1,
 next_run_at timestamptz,
 last_run_at timestamptz,
 last_error text NOT NULL DEFAULT '',
 created_by varchar(120) NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT idx_task_center_schedule_target UNIQUE(node_id,kind,subject)
);
CREATE INDEX idx_task_center_schedules_due ON task_center_schedules (next_run_at) WHERE enabled;

CREATE TABLE task_center_schedule_runs (
 id bigserial PRIMARY KEY,
 schedule_id bigint NOT NULL REFERENCES task_center_schedules(id) ON DELETE CASCADE,
 task_id varchar(36) NOT NULL DEFAULT '',
 triggered_by varchar(16) NOT NULL DEFAULT 'scheduled',
 error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_task_center_schedule_runs_schedule ON task_center_schedule_runs (schedule_id, created_at DESC);

CREATE TABLE task_center_artifacts (
 task_id varchar(36) PRIMARY KEY,
 kind varchar(64) NOT NULL,
 node_id varchar(64) NOT NULL DEFAULT '',
 subject varchar(200) NOT NULL DEFAULT '',
 data jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_task_center_artifacts_lookup ON task_center_artifacts (kind, node_id, subject, created_at DESC);
