-- Read-only verification after business conversion and before accepting traffic.
-- Use the application's schema/search_path. Do not print configuration JSON,
-- credentials, ciphertext, environment values, webhook secrets or free-form errors.
WITH business AS (
  SELECT 'system.cleanup'::text AS kind, p.node_id, ''::text AS subject,
    p.enabled AS legacy_enabled, false AS legacy_webhook, 0 AS legacy_interval,
    (p.config_json::jsonb #>> '{schedule,minute}') || ' ' ||
    (p.config_json::jsonb #>> '{schedule,hour}') || ' * * ' ||
      CASE WHEN p.config_json::jsonb #>> '{schedule,frequency}' = 'weekly'
        THEN p.config_json::jsonb #>> '{schedule,weekday}' ELSE '*' END AS legacy_cron,
    p.config_json::jsonb #>> '{schedule,timezone}' AS legacy_timezone,
    p.next_run_at AS legacy_next_run_at,
    p.authorized_by IS NOT NULL AND p.authorized_at IS NOT NULL AS authorized
  FROM cleanup_policies p
  UNION ALL
  SELECT 'cd.sync', '', p.name, p.automation_enabled, p.webhook_enabled,
    p.sync_interval_seconds, '', 'UTC', NULL::timestamptz, true
  FROM delivery_projects p WHERE p.git_clone_url <> ''
  UNION ALL
  SELECT 'project.cd.check', p.node_id, p.project_name, p.automation_enabled,
    p.webhook_enabled, p.sync_interval_seconds, '', 'UTC', NULL::timestamptz, true
  FROM project_cds p WHERE p.enabled
), comparison AS (
  SELECT b.*, s.id AS rule_id, s.enabled AS rule_enabled,
    s.timer_enabled, s.webhook_enabled AS rule_webhook,
    s.interval_seconds AS rule_interval, s.cron AS rule_cron,
    s.timezone AS rule_timezone, s.next_run_at AS rule_next_run_at,
    s.id IS NOT NULL AND s.enabled = b.legacy_enabled AND
      s.webhook_enabled = b.legacy_webhook AND
      s.interval_seconds = b.legacy_interval AND s.cron = b.legacy_cron AND
      s.timezone = b.legacy_timezone AS matches
  FROM business b LEFT JOIN task_center_schedules s
    ON s.kind = b.kind AND s.node_id = b.node_id AND s.subject = b.subject
      AND s.archived_at IS NULL
), counts AS (
  SELECT kind, count(*) AS rules,
    count(*) FILTER (WHERE archived_at IS NOT NULL) AS archived,
    count(*) FILTER (WHERE archived_at IS NULL AND enabled AND timer_enabled
      AND schedule_type <> 'none' AND next_run_at IS NULL) AS parked
  FROM task_center_schedules GROUP BY kind
), parked AS (
  SELECT s.id, s.kind, s.node_id, s.subject,
    CASE WHEN s.kind NOT IN ('image.update','system.cleanup','cd.sync','project.cd.check')
      THEN 'unknown_kind'
      WHEN s.kind = 'system.cleanup' AND NOT coalesce(c.authorized, false)
      THEN 'authorization_required'
      ELSE 'next_execution_missing' END AS reason
  FROM task_center_schedules s LEFT JOIN comparison c ON c.rule_id = s.id
  WHERE s.archived_at IS NULL AND
    (s.kind NOT IN ('image.update','system.cleanup','cd.sync','project.cd.check') OR
      (s.enabled AND s.timer_enabled AND s.schedule_type <> 'none' AND s.next_run_at IS NULL))
)
SELECT jsonb_build_object(
  'business_conversion_complete', EXISTS (
    SELECT 1 FROM schema_migrations WHERE key = '0012_task_center_unification_business'),
  'rule_counts', coalesce((SELECT jsonb_agg(to_jsonb(c) ORDER BY kind) FROM counts c), '[]'::jsonb),
  'business_comparison', coalesce((SELECT jsonb_agg(to_jsonb(c) ORDER BY kind,node_id,subject) FROM comparison c), '[]'::jsonb),
  'missing_or_mismatched', (SELECT count(*) FROM comparison WHERE NOT matches),
  'parked_rules', coalesce((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM parked p), '[]'::jsonb),
  'legacy_incomplete_triggers', (SELECT count(*) FROM task_center_schedule_runs WHERE reason_code = 'legacy_incomplete')
) AS task_center_upgrade_report;
