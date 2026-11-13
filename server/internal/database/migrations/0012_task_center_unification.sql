-- Preserve all existing task and automation evidence. The application performs
-- business-rule conversion under the startup migration lock before dispatch.
ALTER TABLE tasks ADD COLUMN result_error text NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN parent_task_id varchar(36);
CREATE INDEX idx_tasks_parent_task_id ON tasks(parent_task_id);

ALTER TABLE task_center_schedules ADD COLUMN schedule_type varchar(16) NOT NULL DEFAULT 'cron';
ALTER TABLE task_center_schedules ADD COLUMN interval_seconds integer NOT NULL DEFAULT 0;
ALTER TABLE task_center_schedules ADD COLUMN timer_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE task_center_schedules ADD COLUMN webhook_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE task_center_schedules ADD COLUMN archived_at timestamptz;
ALTER TABLE task_center_schedules DROP CONSTRAINT idx_task_center_schedule_target;
CREATE UNIQUE INDEX idx_task_center_schedule_target ON task_center_schedules(node_id,kind,subject) WHERE archived_at IS NULL;
ALTER TABLE task_center_schedules ADD CONSTRAINT task_center_schedule_type_valid CHECK (
 (schedule_type = 'cron' AND interval_seconds = 0 AND cron <> '') OR
 (schedule_type = 'interval' AND interval_seconds BETWEEN 30 AND 86400 AND cron = '') OR
 (schedule_type = 'none' AND interval_seconds = 0 AND cron = '')
);

ALTER TABLE task_center_schedule_runs ALTER COLUMN schedule_id DROP NOT NULL;
ALTER TABLE task_center_schedule_runs DROP CONSTRAINT task_center_schedule_runs_schedule_id_fkey;
ALTER TABLE task_center_schedule_runs ADD CONSTRAINT task_center_schedule_runs_schedule_id_fkey
 FOREIGN KEY(schedule_id) REFERENCES task_center_schedules(id) ON DELETE RESTRICT;
ALTER TABLE task_center_schedule_runs ADD COLUMN status varchar(16) NOT NULL DEFAULT 'interrupted';
ALTER TABLE task_center_schedule_runs ADD COLUMN reason_code varchar(64) NOT NULL DEFAULT '';
ALTER TABLE task_center_schedule_runs ADD COLUMN source_id varchar(256) NOT NULL DEFAULT '';
ALTER TABLE task_center_schedule_runs ADD COLUMN scheduled_for timestamptz;
ALTER TABLE task_center_schedule_runs ADD COLUMN actor_id bigint;
ALTER TABLE task_center_schedule_runs ADD COLUMN rule_snapshot jsonb;
ALTER TABLE task_center_schedule_runs ADD COLUMN config_version varchar(128) NOT NULL DEFAULT '';
UPDATE task_center_schedule_runs SET status = CASE
 WHEN task_id <> '' THEN 'started' WHEN error <> '' THEN 'start_failed' ELSE 'interrupted' END;
UPDATE task_center_schedule_runs SET reason_code = 'legacy_incomplete'
 WHERE status = 'interrupted';
CREATE UNIQUE INDEX idx_task_center_schedule_occurrence ON task_center_schedule_runs(schedule_id,scheduled_for)
 WHERE triggered_by = 'scheduled' AND scheduled_for IS NOT NULL;
CREATE INDEX idx_task_center_runs_dispatching ON task_center_schedule_runs(schedule_id) WHERE status = 'dispatching';
CREATE INDEX idx_task_center_schedule_runs_task_id ON task_center_schedule_runs(task_id);

-- Task details resolve original project-CD evidence without scanning history.
CREATE INDEX idx_project_cd_records_task_id ON project_cd_records(task_id);
