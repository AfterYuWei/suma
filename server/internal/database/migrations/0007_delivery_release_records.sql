-- Keep an exact private copy before mapping identities or changing lifecycle projections.
ALTER TABLE delivery_releases ADD COLUMN legacy_snapshot_json jsonb;
UPDATE delivery_releases r SET legacy_snapshot_json=to_jsonb(r)-'legacy_snapshot_json';
ALTER TABLE delivery_releases ADD COLUMN canonical_release_id bigint;
ALTER TABLE delivery_releases ADD COLUMN latest_record_id bigint;
ALTER TABLE delivery_target_states ADD COLUMN active_record_id bigint;
ALTER TABLE delivery_deployment_attempts ADD COLUMN record_id bigint;
ALTER TABLE delivery_deployment_attempts ADD COLUMN target_record_id bigint;
ALTER TABLE delivery_deployment_attempts ADD COLUMN previous_record_id bigint;
ALTER TABLE delivery_deployment_attempts ADD COLUMN node_id varchar(64);
ALTER TABLE delivery_deployment_attempts ADD COLUMN node_name varchar(128);
CREATE TABLE delivery_records (
 id bigserial PRIMARY KEY, project_id bigint NOT NULL, release_id bigint NOT NULL,
 restore_records_json text, source_record_id bigint, legacy_attempt_id bigint UNIQUE, legacy_release_id bigint, operation varchar(32) NOT NULL,
 actor_id bigint, actor_name text, trigger_type text, task_id varchar(36),
 status varchar(32) NOT NULL, failure_reason text, health_summary text,
 snapshot_complete boolean NOT NULL DEFAULT false, snapshot_note text,
 config_hash text, image_references text, compose_files_json text, environment_file text,
 environment_ciphertext bytea, worktree_path text, node_ids_json text,
 registry_credential_ids_json text, git_credential_id bigint,
 deployment_timeout bigint, auto_rollback boolean,
 started_at timestamptz, finished_at timestamptz, created_at timestamptz, updated_at timestamptz
);
CREATE INDEX idx_delivery_records_project_id ON delivery_records(project_id,id DESC);
CREATE INDEX idx_delivery_records_release_id ON delivery_records(release_id,id DESC);
CREATE INDEX idx_delivery_records_source_record_id ON delivery_records(source_record_id);
CREATE INDEX idx_delivery_records_task_id ON delivery_records(task_id);
CREATE INDEX idx_delivery_records_status ON delivery_records(status);
CREATE INDEX idx_delivery_deployment_attempts_record_id ON delivery_deployment_attempts(record_id);
CREATE INDEX idx_delivery_target_states_active_record_id ON delivery_target_states(active_record_id);
CREATE INDEX idx_delivery_releases_canonical_release_id ON delivery_releases(canonical_release_id);
CREATE INDEX idx_delivery_releases_latest_record_id ON delivery_releases(latest_record_id);
-- Keep every old ID and all original evidence. Only known Git identities merge.
WITH identities AS (
 SELECT id,min(id) OVER (PARTITION BY project_id,repository_url,commit_sha) AS first_id
 FROM delivery_releases WHERE COALESCE(repository_url,'')<>'' AND commit_sha<>''
)
UPDATE delivery_releases r SET canonical_release_id=i.first_id
FROM identities i WHERE r.id=i.id AND r.id<>i.first_id;
CREATE UNIQUE INDEX idx_delivery_release_git_identity
ON delivery_releases(project_id,repository_url,commit_sha)
WHERE canonical_release_id IS NULL AND COALESCE(repository_url,'')<>'' AND commit_sha<>'';
-- Node attempts are independent historical evidence. Parent operation IDs and
-- policy snapshots were not retained, so do not invent either during backfill.
INSERT INTO delivery_records(project_id,release_id,legacy_release_id,legacy_attempt_id,operation,task_id,status,
 failure_reason,health_summary,snapshot_note,config_hash,image_references,compose_files_json,
 environment_file,environment_ciphertext,worktree_path,node_ids_json,started_at,finished_at,created_at,updated_at)
SELECT r.project_id,COALESCE(r.canonical_release_id,r.id),r.id,a.id,a.operation,a.task_id,a.status,
 a.failure_reason,a.health_summary,'Historical node attempt; complete operation configuration was not recorded',
 r.config_hash,r.image_references,r.compose_files_json,r.environment_file,r.environment_ciphertext,r.worktree_path,
 json_build_array(d.node_id)::text,a.started_at,a.finished_at,a.created_at,a.updated_at
FROM delivery_deployment_attempts a JOIN delivery_release_deployments d ON d.id=a.deployment_id
JOIN delivery_releases r ON r.id=a.target_release_id ORDER BY a.id;
-- Each attempt maps by its original ID; no timestamp grouping is inferred.
UPDATE delivery_deployment_attempts a SET record_id=x.id,node_id=d.node_id,node_name=d.node_name
FROM delivery_records x,delivery_release_deployments d
WHERE x.legacy_attempt_id=a.id AND d.id=a.deployment_id;
INSERT INTO delivery_records(project_id,release_id,legacy_release_id,operation,actor_id,actor_name,trigger_type,
 task_id,status,failure_reason,health_summary,snapshot_note,config_hash,image_references,compose_files_json,
 environment_file,environment_ciphertext,worktree_path,node_ids_json,started_at,finished_at,created_at,updated_at)
SELECT r.project_id,COALESCE(r.canonical_release_id,r.id),r.id,
 CASE WHEN r.trigger_type='rollback' THEN 'manual_rollback' WHEN r.trigger_type='automatic_rollback' THEN 'auto_rollback' ELSE 'deploy' END,
 NULL,'',CASE WHEN r.trigger_type IN ('rollback','automatic_rollback') THEN r.trigger_type ELSE 'legacy' END,r.task_id,r.status,r.failure_reason,r.health_summary,
 'Historical release; target and execution policy snapshots are incomplete',r.config_hash,r.image_references,
 r.compose_files_json,r.environment_file,r.environment_ciphertext,r.worktree_path,
 (SELECT COALESCE(json_agg(d.node_id ORDER BY d.node_id),'[]'::json)::text FROM delivery_release_deployments d WHERE d.release_id=r.id),
 r.started_at,r.finished_at,r.created_at,r.updated_at
FROM delivery_releases r WHERE (r.started_at IS NOT NULL OR r.status IN ('succeeded','failed','partial_failed','rolled_back','interrupted','pulling','deploying','verifying','rolling_back'))
AND NOT EXISTS (SELECT 1 FROM delivery_release_deployments d JOIN delivery_deployment_attempts a ON a.deployment_id=d.id WHERE d.release_id=r.id);
UPDATE delivery_releases r SET latest_record_id=(SELECT x.id FROM delivery_records x WHERE x.release_id=r.id ORDER BY COALESCE(x.started_at,x.created_at) DESC NULLS LAST,x.id DESC LIMIT 1)
WHERE r.canonical_release_id IS NULL;
UPDATE delivery_target_states s SET active_record_id=(SELECT a.record_id FROM delivery_deployment_attempts a
 JOIN delivery_release_deployments d ON d.id=a.deployment_id
 WHERE d.node_id=s.node_id AND a.target_release_id=s.active_release_id AND a.status IN ('succeeded','rolled_back')
 ORDER BY a.finished_at DESC NULLS LAST,a.id DESC LIMIT 1);
UPDATE delivery_target_states s SET active_record_id=(SELECT x.id FROM delivery_records x WHERE x.legacy_release_id=s.active_release_id ORDER BY x.finished_at DESC NULLS LAST,x.id DESC LIMIT 1) WHERE s.active_record_id IS NULL;
UPDATE delivery_target_states s SET active_release_id=r.canonical_release_id FROM delivery_releases r WHERE s.active_release_id=r.id AND r.canonical_release_id IS NOT NULL;
UPDATE delivery_projects p SET active_release_id=r.canonical_release_id FROM delivery_releases r WHERE p.active_release_id=r.id AND r.canonical_release_id IS NOT NULL;
-- Preserve original decision evidence in approved_by/approved_at and alias rows.
UPDATE delivery_releases SET status='ready' WHERE canonical_release_id IS NULL AND status IN ('awaiting_approval','approved');

-- A historical node attempt is not a parent operation outcome. Derive the
-- canonical lifecycle from the most recently updated original Release row.
WITH latest AS (
 SELECT DISTINCT ON (COALESCE(canonical_release_id,id)) COALESCE(canonical_release_id,id) AS canonical_id,
 status,task_id,failure_reason,health_summary,started_at,finished_at
 FROM delivery_releases
 ORDER BY COALESCE(canonical_release_id,id),GREATEST(updated_at,started_at,finished_at,created_at) DESC NULLS LAST,id DESC
)
UPDATE delivery_releases r SET status=CASE WHEN x.status IN ('awaiting_approval','approved') THEN 'ready' ELSE x.status END,
 task_id=x.task_id,failure_reason=x.failure_reason,health_summary=x.health_summary,started_at=x.started_at,finished_at=x.finished_at
FROM latest x WHERE r.id=x.canonical_id;
