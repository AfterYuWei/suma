ALTER TABLE project_cds
 ADD COLUMN reconcile_mode varchar(16) NOT NULL DEFAULT 'manual',
 ADD COLUMN auto_rollback boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT project_cd_reconcile_mode CHECK (reconcile_mode IN ('observe', 'manual', 'auto'));

-- Preserve the existing automatic-apply behavior. Historical snapshots are
-- untouched; a missing rollback policy in an old snapshot remains disabled.
UPDATE project_cds SET reconcile_mode = 'auto' WHERE automation_enabled = true;

ALTER TABLE project_cd_records ADD COLUMN source_record_id bigint;
