CREATE TABLE project_image_updates (
    node_id varchar(64) NOT NULL, project varchar(128) NOT NULL,
    version bigint NOT NULL DEFAULT 0, mode text NOT NULL DEFAULT 'inherit',
    cron text NOT NULL DEFAULT '0 */6 * * *', timezone text NOT NULL DEFAULT 'UTC',
    next_run_at timestamptz, last_checked_at timestamptz,
    last_error text NOT NULL DEFAULT '', results_json text NOT NULL DEFAULT '[]',
    runtime_key text NOT NULL DEFAULT '', PRIMARY KEY (node_id, project),
    CHECK (mode IN ('inherit','custom','disabled'))
);
CREATE TABLE project_image_candidates (
    id varchar(64) PRIMARY KEY, node_id varchar(64) NOT NULL, project varchar(128) NOT NULL,
    requires_docker_socket boolean NOT NULL DEFAULT false, fingerprint text NOT NULL, revision text NOT NULL, runtime_key text NOT NULL,
    services_json text NOT NULL, status text NOT NULL, task_id text NOT NULL DEFAULT '',
    actor_json text NOT NULL DEFAULT '{}', message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE INDEX idx_project_image_candidates_node_id ON project_image_candidates(node_id);
CREATE UNIQUE INDEX project_image_candidate_active ON project_image_candidates(node_id,project) WHERE status IN ('pending','publishing');
CREATE TABLE project_image_actions (
    state text NOT NULL DEFAULT 'pending', token_hash varchar(64) PRIMARY KEY, candidate_id varchar(64) NOT NULL,
    delivery_id varchar(64) NOT NULL, channel_id varchar(64) NOT NULL,
    chat_id varchar(128) NOT NULL, destination_version bigint NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE INDEX idx_project_image_actions_candidate_id ON project_image_actions(candidate_id);
CREATE INDEX idx_project_image_actions_delivery_id ON project_image_actions(delivery_id);
